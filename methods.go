package poly

import (
	"log/slog"
	"maps"
	"time"

	"github.com/jpl-au/fluent-poly/push"
)

// State returns the current session state. When called from inside
// Handle (or a goroutine it spawned) it returns an atomic snapshot
// captured at the start of the exec cycle — no channel hop, no race.
// When called from outside Handle, it performs a synchronous read
// through the command channel so the value reflects any prior queued
// updates.
//
// If the command loop has not started yet (e.g. during OnConnect),
// the state is returned directly. This is safe because no concurrent
// mutations can occur before the loop is running.
func (s *Session[S]) State() S {
	if s.handling.Load() {
		return s.stateSnap.Load().(S)
	}
	if !s.loopRunning.Load() {
		// Loop not started — return state directly to avoid
		// deadlocking on a channel nobody is draining.
		return s.state
	}
	ch := make(chan S, 1)
	select {
	case s.cmds <- func() { ch <- s.state }:
		return <-ch
	case <-s.ctx.Done():
		// Session destroyed — return last known state rather than
		// blocking forever on a channel nobody will drain.
		return s.state
	}
}

// Update applies a state change and pushes the resulting diff to the
// client. This is the primary way to push server-initiated updates —
// call it from timers, database change listeners, message queue
// consumers, or [Group.Broadcast].
//
// A full render-diff-send cycle is queued as a command and runs after
// the current event (if any) has been fully processed. This means
// that when called inside Handle, the update does not take effect
// until Handle returns — the Handle return value is always
// authoritative for the triggering event. Non-blocking — returns
// immediately after queuing.
//
// Inside Handle, prefer returning the new state directly rather than
// calling Update. Update is designed for side-effects like broadcasts
// where the caller does not control Handle's return value.
//
// Safe to call from any goroutine, including from within Handle.
func (s *Session[S]) Update(fn func(S) S) {
	s.enqueue(func() {
		s.stateSnap.Store(s.state)
		s.handling.Store(true)
		fx := &effects{}
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in Update", "session", s.id, "panic", r)
				s.drainFx(nil)
			}
			s.handling.Store(false)
		}()

		s.lastActivity.Store(time.Now().UnixNano())
		if s.idleTimer != nil {
			s.idleTimer.Reset(s.idleTimeout)
		}
		s.state = fn(s.state)

		// Collect effects enqueued during fn.
		s.drainFx(fx)

		tree := s.render(s.state)
		patches, change := s.differ.Diff(tree)
		s.sendDiff("", patches, change, tree, fx)
	})
}

// Close terminates the session by closing its transport. The reader
// goroutine exits, which closes the events channel, which the loop
// handles via onTransportClose. Safe to call from any goroutine;
// safe to call more than once.
func (s *Session[S]) Close() {
	s.enqueue(func() {
		if s.transport != nil {
			s.transport.Close()
		}
	})
}

// Toast sends a global notification to the client. Inside Handle the
// toast is buffered and sent atomically with the state diff. Outside
// Handle it is sent as a standalone update.
func (s *Session[S]) Toast(text string) {
	s.enqueueFx(func(fx *effects) { fx.toast = text })
}

// Navigate pushes a URL change to the client (history.pushState).
// Inside Handle the URL is buffered; outside it is sent as a
// standalone update.
func (s *Session[S]) Navigate(rawURL string) {
	s.enqueueFx(func(fx *effects) {
		fx.url = rawURL
		fx.replace = false
	})
}

// ReplaceURL updates the browser URL without a history entry
// (history.replaceState). Inside Handle the URL is buffered; outside
// it is sent as a standalone update.
func (s *Session[S]) ReplaceURL(rawURL string) {
	s.enqueueFx(func(fx *effects) {
		fx.url = rawURL
		fx.replace = true
	})
}

// SetTitle updates the browser's document title. Inside Handle the
// title is buffered; outside it is sent as a standalone update.
func (s *Session[S]) SetTitle(title string) {
	s.enqueueFx(func(fx *effects) { fx.title = title })
}

// Announce sends text to a screen-reader-accessible live region on
// the client. Inside Handle the text is buffered; outside it is sent
// as a standalone update.
func (s *Session[S]) Announce(text string) {
	s.enqueueFx(func(fx *effects) { fx.announce = text })
}

// Flash sends a one-time notification to the client. The selector is
// a CSS selector for the target element; the text is displayed for 5
// seconds. Inside Handle the flash is buffered; outside it is sent
// as a standalone update.
func (s *Session[S]) Flash(selector, text string) {
	s.enqueueFx(func(fx *effects) {
		if fx.flash == nil {
			fx.flash = make(map[string]string)
		}
		fx.flash[selector] = text
	})
}

// Signal pushes a reactive value to the client. Elements bound to the
// signal name via [BindText], [BindShow], [BindClass], or [BindAttr]
// update instantly — no render cycle, no diff, no HTML. Inside Handle
// the signal is buffered and sent atomically with the state diff.
// Outside Handle it is sent as a standalone update.
//
// Signals are ideal for high-frequency updates (counters, status
// indicators, progress bars) where the full render/diff pipeline
// is unnecessary overhead.
//
//	s.Signal("count", 42)
//	s.Signal("status", "online")
func (s *Session[S]) Signal(key string, value any) {
	s.enqueueFx(func(fx *effects) {
		if fx.signals == nil {
			fx.signals = make(map[string]any)
		}
		fx.signals[key] = value
	})
}

// Signals pushes multiple reactive values to the client in a single
// update. This is a batch variant of [Signal] — use it when setting
// several signals at once to avoid sending one message per key.
// Inside Handle all keys are merged into the buffered effects.
// Outside Handle a single update is sent with all keys.
//
//	s.Signals(map[string]any{"count": 42, "status": "online"})
func (s *Session[S]) Signals(signals map[string]any) {
	s.enqueueFx(func(fx *effects) {
		if fx.signals == nil {
			fx.signals = make(map[string]any, len(signals))
		}
		maps.Copy(fx.signals, signals)
	})
}

// SignalBatch pushes multiple reactive values using a flat key-value
// list. This is a convenience wrapper around [Session.Signals] that
// avoids the map literal syntax for small batches:
//
//	s.SignalBatch("count", 42, "status", "online")
//
// Panics if an odd number of arguments is provided or if any key is
// not a string.
func (s *Session[S]) SignalBatch(pairs ...any) {
	s.Signals(pairsToMap(pairs))
}

// Push sends a Web Push notification to the browser. Only works when
// the session has an active push subscription and a [push.Sender] is
// configured. Returns an error if either is missing.
//
// Safe to call from any goroutine — pushSender is immutable and
// pushSub is an atomic pointer, so no command-channel round-trip is
// needed.
func (s *Session[S]) Push(n push.Notification) error {
	if s.pushSender == nil {
		return ErrPushNotConfigured
	}
	sub := s.pushSub.Load()
	if sub == nil {
		return ErrPushNoSubscription
	}
	return s.pushSender.Send(*sub, n)
}
