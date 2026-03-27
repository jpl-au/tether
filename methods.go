package tether

import (
	"maps"
	"time"

	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/push"
	"github.com/jpl-au/tether/wire"
)

// State returns the current session state. Never blocks.
//
// When the loop is active, State returns an atomic snapshot updated
// after every state mutation (Handle return, Update callback). The
// snapshot is lock-free and safe to call from any goroutine at any
// time.
//
// Do not call State() inside Handle - the snapshot is stale (it
// reflects the state before Handle was called). Use the state
// parameter passed to Handle instead. In dev mode, a warning is
// emitted if State() is called during Handle to help catch this
// mistake early.
//
// When the loop is not active (before startup, after destruction, or
// while frozen), the state field is returned directly - no concurrent
// mutations are possible.
func (s *StatefulSession[S]) State() S {
	if s.handling {
		dev.Warn("State() called during Handle - the returned value is stale; use the state parameter instead",
			"session", s.id, "endpoint", s.endpoint)
	}
	if Status(s.status.Load()) != Active {
		return s.state
	}
	if v := s.stateSnap.Load(); v != nil {
		return v.(S)
	}
	return s.state
}

// Update applies a state change and pushes the resulting diff to the
// client. This is the primary way to push server-initiated updates -
// call it from timers, database change listeners, message queue
// consumers, or [Group.Broadcast].
//
// The state mutation is queued as a command and runs after the current
// event (if any) has been fully processed. Multiple rapid Updates
// (e.g. from a broadcast storm) are coalesced - mutations execute
// sequentially but only one render-diff-send cycle runs for the batch.
// This means that when called inside Handle, the update does not take
// effect until Handle returns - the Handle return value is always
// authoritative for the triggering event. Non-blocking - returns
// immediately after queuing.
//
// The diff engine only tracks elements marked with .Dynamic("key").
// If the state change affects elements that lack a Dynamic key, the
// diff will produce no patches and the client will not update. Wrap
// state-dependent content in a container with a stable Dynamic key:
//
//	div.New(uploadList(s.Files)).Dynamic("uploads")
//
// In DevMode, a warning is logged when Update produces no patches.
//
// Inside Handle, prefer returning the new state directly rather than
// calling Update. Update is designed for side-effects like broadcasts
// where the caller does not control Handle's return value.
//
// Safe to call from any goroutine, including from within Handle.
func (s *StatefulSession[S]) Update(fn func(S) S) {
	s.enqueue(func() {
		s.stateSnap.Store(s.state)
		defer func() {
			if r := recover(); r != nil {
				err := panicErr(r)
				dev.Log().Error("panic in Update", "session", s.id, "panic", r)
				s.emitDiagnostic(Diagnostic{
					Kind:      HandlerPanic,
					SessionID: s.id,
					Err:       err,
					Detail:    s.endpoint,
				})
				s.needsRender = false
				s.drainFx(nil)
				if s.onPanic != nil {
					s.onPanic(s, err)
				} else {
					s.stop()
				}
			}
		}()

		s.lastActivity.Store(time.Now().UnixNano())
		if s.idleTimer != nil {
			s.idleTimer.Reset(s.idleTimeout)
		}
		s.state = fn(s.state)
		s.stateSnap.Store(s.state)
		s.needsRender = true
	})
}

// Patch applies a targeted state change and re-renders a single
// Dynamic key. Unlike [Update] which re-renders the full tree, Patch
// only diffs the targeted key against the stored snapshot. Over
// 1,000x faster than Update for targeting one key out of many.
//
// Patch works with either engine (Differ or Memoiser). It does not
// require [StatefulConfig].Memo to be true. Any handler with Dynamic
// keys can use it.
//
// The closure receives the current state and returns the new state
// plus the rendered subtree for the target key. Both come from the
// same closure so they cannot get out of sync.
//
// Inside Handle, prefer returning the new state directly - Handle
// already triggers a full render. Patch is for server-initiated
// updates where a full render is unnecessary.
//
// In dev mode, a warning is emitted if the key has no stored
// snapshot, which usually indicates a typo.
//
// Safe to call from any goroutine.
func (s *StatefulSession[S]) Patch(key string, fn func(S) (S, node.Node)) {
	s.enqueue(func() {
		s.stateSnap.Store(s.state)
		defer func() {
			if r := recover(); r != nil {
				err := panicErr(r)
				dev.Log().Error("panic in Patch", "session", s.id, "key", key, "panic", r)
				s.emitDiagnostic(Diagnostic{
					Kind:      HandlerPanic,
					SessionID: s.id,
					Err:       err,
					Detail:    "patch:" + key,
				})
				if s.onPanic != nil {
					s.onPanic(s, err)
				} else {
					s.stop()
				}
			}
		}()

		s.lastActivity.Store(time.Now().UnixNano())
		if s.idleTimer != nil {
			s.idleTimer.Reset(s.idleTimeout)
		}

		newState, subtree := fn(s.state)
		s.state = newState
		s.stateSnap.Store(s.state)

		renderStart := time.Now()
		patch := s.engine.DiffKey(key, subtree)
		renderDuration := time.Since(renderStart)
		dev.Debug("patch complete",
			"session", s.id,
			"key", key,
			"changed", patch != nil,
			"duration", renderDuration,
		)
		if s.slowRender > 0 && renderDuration > s.slowRender {
			s.emitDiagnostic(Diagnostic{
				Kind:      SlowRender,
				SessionID: s.id,
				Detail:    renderDuration.String(),
			})
		}

		if patch != nil {
			s.send(wire.Update{
				Patches: []wire.Patch{{Key: patch.Key, HTML: patch.HTML}},
			})
		}
	})
}

// Close terminates the session by closing its transport. The reader
// goroutine exits, which closes the events channel, which the loop
// handles via onTransportClose. Safe to call from any goroutine;
// safe to call more than once.
func (s *StatefulSession[S]) Close() {
	s.enqueue(func() {
		if s.transport != nil {
			s.transport.Close()
		}
	})
}

// Toast sends a global notification to the client. Inside Handle the
// toast is buffered and sent atomically with the state diff. Outside
// Handle it is sent as a standalone update.
func (s *StatefulSession[S]) Toast(text string) {
	s.enqueueFx(func(fx *Effects) { fx.Toast = text })
}

// Navigate pushes a URL change to the client (history.pushState).
// Inside Handle the URL is buffered; outside it is sent as a
// standalone update.
func (s *StatefulSession[S]) Navigate(rawURL string) {
	s.enqueueFx(func(fx *Effects) {
		fx.URL = rawURL
		fx.Replace = false
	})
}

// ReplaceURL updates the browser URL without a history entry
// (history.replaceState). Inside Handle the URL is buffered; outside
// it is sent as a standalone update.
func (s *StatefulSession[S]) ReplaceURL(rawURL string) {
	s.enqueueFx(func(fx *Effects) {
		fx.URL = rawURL
		fx.Replace = true
	})
}

// SetTitle updates the browser's document title. Inside Handle the
// title is buffered; outside it is sent as a standalone update.
func (s *StatefulSession[S]) SetTitle(title string) {
	s.enqueueFx(func(fx *Effects) { fx.Title = title })
}

// Announce sends text to a screen-reader-accessible live region on
// the client. Inside Handle the text is buffered; outside it is sent
// as a standalone update.
func (s *StatefulSession[S]) Announce(text string) {
	s.enqueueFx(func(fx *Effects) { fx.Announce = text })
}

// Flash sends a one-time notification to the client. The selector is
// a CSS selector for the target element; the text is displayed for 5
// seconds. Inside Handle the flash is buffered; outside it is sent
// as a standalone update.
func (s *StatefulSession[S]) Flash(selector, text string) {
	s.enqueueFx(func(fx *Effects) {
		if fx.Flash == nil {
			fx.Flash = make(map[string]string)
		}
		fx.Flash[selector] = text
	})
}

// ScrollTo scrolls the matched element into view on the client.
// Inside Handle the command is buffered; outside it is sent as a
// standalone update.
func (s *StatefulSession[S]) ScrollTo(selector string) {
	s.enqueueFx(func(fx *Effects) { fx.ScrollTo = selector })
}

// Download triggers a file download in the browser from the given
// URL. The browser makes a normal HTTP GET - the file is served via
// standard HTTP, not over the WebSocket. Inside Handle the URL is
// buffered; outside it is sent as a standalone update.
func (s *StatefulSession[S]) Download(url string) {
	s.enqueueFx(func(fx *Effects) { fx.Download = url })
}

// Signal pushes a reactive value to the client. Elements bound to the
// signal name via [BindText], [BindShow], [BindClass], or [BindAttr]
// update instantly - no render cycle, no diff, no HTML. Inside Handle
// the signal is buffered and sent atomically with the state diff.
// Outside Handle it is sent as a standalone update.
//
// Signals are ideal for high-frequency updates (counters, status
// indicators, progress bars) where the full render/diff pipeline
// is unnecessary overhead.
//
//	s.Signal("count", 42)
//	s.Signal("status", "online")
func (s *StatefulSession[S]) Signal(key string, value any) {
	s.enqueueFx(func(fx *Effects) {
		if fx.Signals == nil {
			fx.Signals = make(map[string]any)
		}
		fx.Signals[key] = value
	})
}

// Signals pushes multiple reactive values to the client in a single
// update. This is a batch variant of [Signal] - use it when setting
// several signals at once to avoid sending one message per key.
// Inside Handle all keys are merged into the buffered effects.
// Outside Handle a single update is sent with all keys.
//
//	s.Signals(map[string]any{"count": 42, "status": "online"})
func (s *StatefulSession[S]) Signals(signals map[string]any) {
	s.enqueueFx(func(fx *Effects) {
		if fx.Signals == nil {
			fx.Signals = make(map[string]any, len(signals))
		}
		maps.Copy(fx.Signals, signals)
	})
}

// Push sends a Web Push notification to the browser. Only works when
// the session has an active push subscription and a [push.Sender] is
// configured. Returns an error if either is missing.
//
// Safe to call from any goroutine - pushSender is immutable and
// pushSub is an atomic pointer, so no command-channel round-trip is
// needed.
func (s *StatefulSession[S]) Push(n push.Notification) error {
	if s.pushSender == nil {
		return ErrPushNotConfigured
	}
	sub := s.pushSub.Load()
	if sub == nil {
		return ErrPushNoSubscription
	}
	return s.pushSender.Send(*sub, n)
}
