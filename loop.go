package tether

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// maxNavigateRedirects caps how many consecutive Navigate calls the
// framework resolves inline during a single navigate event. This
// prevents infinite loops when OnNavigate unconditionally redirects.
const maxNavigateRedirects = 5

// run is the session's command loop. It processes transport events,
// commands from external callers, and effect closures in a single
// goroutine - no mutex needed. The loop exits when the session
// context is cancelled (shutdown, reaper timeout, or explicit
// destruction).
//
// When the transport closes, the events channel is nilled so the loop
// continues processing commands and shutdown signals. This keeps the
// session alive for reconnection.
func (s *StatefulSession[S]) run() {
	dev.Debug("run loop started", "session", s.id, "endpoint", s.endpoint)
	s.stateSnap.Store(s.state)
	s.transition(Active)
	defer close(s.loopDone)
	defer s.cleanup()

	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				// Transport closed. Nil the channel so select
				// skips it. Session stays alive for reconnection.
				s.events = nil
				s.onTransportClose()
				if s.freeze {
					// Frozen - exit the loop. State has been
					// persisted and memory released. A reconnect
					// will thaw by starting a new run().
					return
				}
				continue
			}
			s.exec(ev)

		case cmd := <-s.cmds:
			s.runCmd(cmd)

		case fn := <-s.fxCh:
			// Effect arriving outside of Handle - send immediately.
			// Reset idle timer: the server is actively communicating
			// with the client, so the session is not idle.
			s.lastActivity.Store(time.Now().UnixNano())
			if s.idleTimer != nil {
				s.idleTimer.Reset(s.idleTimeout)
			}
			fx := &Effects{}
			fn(fx)
			s.sendFx(fx)

		case <-s.ctx.Done():
			return
		}
	}
}

// runCmd executes a command with panic recovery so a misbehaving
// command cannot crash the entire process.
func (s *StatefulSession[S]) runCmd(cmd func()) {
	defer func() {
		if r := recover(); r != nil {
			err := panicErr(r)
			dev.Log().Error("panic in command", "session", s.id, "panic", r)
			s.emitDiagnostic(Diagnostic{
				Kind:      HandlerPanic,
				SessionID: s.id,
				Err:       err,
				Detail:    s.endpoint,
			})
			if s.onPanic != nil {
				s.onPanic(s, err)
			} else {
				s.stop()
			}
		}
	}()
	cmd()
}

// readTransport bridges the blocking ReceiveEvent call into the
// events channel. Its only job is to read and forward - it closes
// the output channel on exit so the loop knows the transport is gone.
func (s *StatefulSession[S]) readTransport(out chan<- Event) {
	dev.Debug("readTransport started", "session", s.id, "endpoint", s.endpoint)
	defer close(out)
	for {
		ev, err := s.transport.ReceiveEvent()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.emitDiagnostic(Diagnostic{
					Kind:      TransportError,
					SessionID: s.id,
					Err:       err,
					Detail:    s.endpoint,
				})
			}
			return
		}
		out <- ev
	}
}

// exec processes a single client event: handle it, re-render, diff,
// and send patches to the transport.
func (s *StatefulSession[S]) exec(ev Event) {
	now := time.Now()
	s.lastActivity.Store(now.UnixNano())
	if s.idleTimer != nil {
		s.idleTimer.Reset(s.idleTimeout)
	}

	s.stateSnap.Store(s.state)
	fx := &Effects{}
	defer func() {
		if r := recover(); r != nil {
			err := panicErr(r)
			dev.Log().Error("panic in handler", "session", s.id, "action", ev.Action, "panic", r)
			s.emitDiagnostic(Diagnostic{
				Kind:      HandlerPanic,
				SessionID: s.id,
				Err:       err,
				Detail:    ev.Action,
			})
			s.drainFx(nil)
			// State may contain partially mutated reference types
			// (maps, slices) that cannot be trusted. Destroy the
			// session unless the developer has opted into custom
			// recovery via OnPanic.
			if s.onPanic != nil {
				s.onPanic(s, err)
			} else {
				s.stop()
			}
		}
	}()

	dev.Debug("event received",
		"session", s.id,
		"endpoint", s.endpoint,
		"action", ev.Action,
		"type", ev.Type,
	)

	// All events flow through the composed Handle function, which
	// includes middleware, OnNavigate, and component routing.
	s.handling = true
	newState := s.handle(s, s.state, ev)
	s.handling = false

	s.drainFx(fx)

	// Resolve navigate redirects inline. When OnNavigate calls
	// Navigate(), re-process the redirect target server-side rather
	// than round-tripping to the client. Effects from intermediate
	// steps are preserved unless the redirect target overwrites them.
	if ev.Type == "navigate" && fx.URL != "" {
		for i := range maxNavigateRedirects {
			redirectURL := fx.URL
			u, err := url.Parse(redirectURL)
			if err != nil {
				dev.Warn("malformed navigate redirect URL",
					"session", s.id, "url", redirectURL, "error", err)
				break
			}

			fx.URL = ""
			fx.Replace = false
			redirectEv := Event{
				Type: "navigate",
				Data: map[string]string{"path": u.Path, "search": u.RawQuery},
			}
			newState = s.handle(s, newState, redirectEv)
			s.drainFx(fx)

			if fx.URL == "" {
				// Redirect resolved - send the target URL as a replace
				// so the client updates the address bar without a
				// history entry or a navigate event back.
				fx.URL = redirectURL
				fx.Replace = true
				break
			}
			if i == maxNavigateRedirects-1 {
				dev.Warn("navigate redirect limit reached",
					"session", s.id, "url", fx.URL)
				s.emitDiagnostic(Diagnostic{
					Kind:      NavigateRedirectLoop,
					SessionID: s.id,
					Err:       fmt.Errorf("redirect limit exceeded after %d redirects", maxNavigateRedirects),
					Detail:    fx.URL,
				})
				fx.Replace = true
			}
		}
	}

	if s.equal != nil && s.equal(s.state, newState) {
		dev.Debug("state unchanged, skipping render",
			"session", s.id,
			"action", ev.Action,
		)
		if fx.Any() || ev.EventID != "" {
			u := wire.Update{EventID: ev.EventID}
			fx.merge(&u)
			s.send(u)
		}
		return
	}
	s.state = newState
	s.stateSnap.Store(s.state)

	tree := s.render(s.state)
	patches, change := s.differ.Diff(tree)
	dev.Debug("render complete",
		"session", s.id,
		"patches", len(patches),
		"structural", change != nil,
	)
	if len(patches) == 0 && change == nil {
		if s.onNoPatch != nil {
			source := "event"
			if ev.Type == "navigate" {
				source = "navigate"
			}
			s.onNoPatch(s, NoPatch{Source: source, Action: ev.Action})
		} else if ev.Type == "navigate" {
			dev.Debug("navigate produced no patches",
				"session", s.id,
				"endpoint", s.endpoint,
				"url", s.lastURL,
			)
		} else {
			dev.Debug("event produced no patches",
				"session", s.id,
				"endpoint", s.endpoint,
				"action", ev.Action,
			)
		}
	}

	s.sendDiff(ev.EventID, patches, change, tree, fx)
}

// onTransportClose runs when the client's transport connection drops.
// It nils the transport so send() discards updates during the
// reconnect window, then persists session data to any configured
// stores - DiffStore for differ snapshots (memory optimisation) and
// SessionStore for application state (crash recovery). Persistence
// happens before the pool transition so data is safely stored before
// the session becomes visible as reconnectable.
func (s *StatefulSession[S]) onTransportClose() {
	dev.Debug("transport closed",
		"session", s.id,
		"endpoint", s.endpoint,
		"url", s.lastURL,
	)
	s.transport.Close()
	s.transport = nil
	if s.transportCancel != nil {
		s.transportCancel()
	}

	if s.reconnectTimeout > 0 {
		dev.Debug("disconnect timer started",
			"session", s.id,
			"endpoint", s.endpoint,
			"timeout", s.reconnectTimeout,
		)
		s.disconnectTimer = time.AfterFunc(s.reconnectTimeout, func() {
			if s.handler != nil {
				s.handler.sessionTimedOut(s)
			} else {
				s.stop()
			}
		})
	}

	// Save differ snapshots to the store before the pool transition
	// so the data is persisted before the session becomes visible as
	// reconnectable. Export copies without clearing; Clear is only
	// called after a confirmed successful save.
	if s.store != nil {
		if data := s.differ.Export(); data != nil {
			if err := s.store.Save(s.ctx, s.id, data); err != nil {
				dev.Warn("store save failed, keeping snapshots in memory",
					"session", s.id, "error", err)
				s.emitDiagnostic(Diagnostic{
					Kind:      StoreError,
					SessionID: s.id,
					Err:       err,
					Detail:    "save",
				})
			} else {
				s.differ.Clear()
			}
		}
	}

	// Save session state for crash recovery. The codec serialises S,
	// the envelope wraps it with metadata, and the store persists the
	// bytes. TTL matches the reconnect window - if the client never
	// comes back, the store entry can expire.
	if s.sessionStore != nil {
		s.saveSessionState(s.ctx, s.reconnectTimeout)
	}

	if s.handler != nil {
		s.handler.sessionDisconnected(s)
	}

	// Freeze: release state and differ to reclaim memory. The store
	// holds everything needed to restore. The loop exits after this
	// returns (checked by the caller in run).
	if s.freeze {
		var zero S
		s.state = zero
		s.differ = nil
		s.transition(Frozen)
		dev.Debug("session frozen", "session", s.id, "endpoint", s.endpoint)
	}
}

// saveSessionState encodes the session's state and metadata into an
// envelope and persists it to the SessionStore. The caller provides
// the context - onTransportClose passes s.ctx (still valid during
// disconnect), Shutdown passes context.Background() (s.ctx is
// cancelled after the loop exits). Failures are logged and emitted
// as diagnostics but are non-fatal.
func (s *StatefulSession[S]) saveSessionState(ctx context.Context, ttl time.Duration) {
	stateBytes, err := s.codec.Marshal(s.state)
	if err != nil {
		dev.Warn("session state marshal failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "marshal",
		})
		return
	}

	env := sessionEnvelope{
		State:     stateBytes,
		Endpoint:  s.endpoint,
		URL:       s.lastURL,
		Title:     s.lastTitle,
		UserAgent: s.userAgent,
	}
	data, err := marshalEnvelope(env)
	if err != nil {
		dev.Warn("session envelope marshal failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "envelope",
		})
		return
	}

	if err := s.sessionStore.Save(ctx, s.id, data, ttl); err != nil {
		dev.Warn("session store save failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "save",
		})
	}
}

// cleanup runs when the loop exits. For frozen sessions only the
// idle timer is stopped - the disconnect timer keeps running so the
// reaper can destroy the session if it is never thawed. For
// destroyed sessions, everything is stopped and the destroyed
// channel is closed.
//
// cleanup runs when the loop exits. For frozen sessions only the
// idle timer is stopped - the disconnect timer keeps running so the
// reaper can destroy the session if it is never thawed. For
// destroyed sessions, everything is stopped and the destroyed
// channel is closed via destroyedOnce.
func (s *StatefulSession[S]) cleanup() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	if Status(s.status.Load()) == Frozen {
		return
	}
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
	}
	if s.lifetimeTimer != nil {
		s.lifetimeTimer.Stop()
	}
	if s.transport != nil {
		s.transport.Close()
	}
	s.transition(Destroyed)
	s.destroyedOnce.Do(func() { close(s.destroyed) })
}

// sendDiff is the render-diff-send pipeline extracted from exec and
// Update. It handles patches, structural changes, and the no-diff
// case where only the eventID needs echoing. fx carries any buffered
// effects to merge into the update message.
func (s *StatefulSession[S]) sendDiff(eventID string, patches []jit.Patch, change *jit.StructuralChange, tree node.Node, fx *Effects) {
	if change != nil {
		html := s.differ.Render(tree)
		sc := StructuralChange{
			Added:     change.Added,
			Removed:   change.Removed,
			Reordered: change.Reordered,
			Bytes:     len(html),
		}
		if s.onStructuralChange != nil {
			s.onStructuralChange(s, sc)
		} else {
			dev.Debug("structural change, sending root morph",
				"session", s.id,
				"endpoint", s.endpoint,
				"change", change.String(),
				"bytes", len(html),
			)
		}

		u := wire.Update{
			Morphs:  []wire.Morph{{Key: "", HTML: html}},
			EventID: eventID,
		}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

	if len(patches) > 0 || (fx != nil && fx.Any()) {
		wp := make([]wire.Patch, len(patches))
		for i, p := range patches {
			wp[i] = wire.Patch{Key: p.Key, HTML: p.HTML}
		}
		u := wire.Update{Patches: wp, EventID: eventID}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

	// No patches and no structural change. Still echo the eventID
	// so the client can restore any loading state.
	if eventID != "" {
		s.send(wire.Update{EventID: eventID})
	}
}

// send encodes an update and writes the bytes to the transport. URL
// and title are captured before the nil-transport guard so reattach
// can replay them after a disconnect.
func (s *StatefulSession[S]) send(u wire.Update) {
	if u.URL != "" {
		s.lastURL = u.URL
	}
	if u.Title != "" {
		s.lastTitle = u.Title
	}
	if s.transport == nil {
		return
	}
	data, err := s.encoder.Encode(u)
	if err != nil {
		s.emitDiagnostic(Diagnostic{
			Kind:      EncodeError,
			SessionID: s.id,
			Err:       err,
			Detail:    s.endpoint,
		})
		return
	}
	if err := s.transport.Send(data); err != nil {
		s.emitDiagnostic(Diagnostic{
			Kind:      TransportError,
			SessionID: s.id,
			Err:       err,
			Detail:    s.endpoint,
		})
	}
}

// startTimers sets up per-session lifecycle timers. Called once during
// session creation in serve.go. Timer cleanup happens in [cleanup],
// which runs as a defer in [run] when the loop exits.
func (s *StatefulSession[S]) startTimers() {
	if s.idleTimeout > 0 {
		s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
			s.stop()
		})
	}
}
