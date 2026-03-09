package tether

import (
	"errors"
	"io"
	"log/slog"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent-tether/dev"
	"github.com/jpl-au/fluent-tether/wire"
	"github.com/jpl-au/fluent/node"
)

// run is the session's command loop. It processes transport events,
// commands from external callers, and effect closures in a single
// goroutine — no mutex needed. The loop exits when the session
// context is cancelled (shutdown, reaper timeout, or explicit
// destruction).
//
// When the transport closes, the events channel is nilled so the loop
// continues processing commands and shutdown signals. This keeps the
// session alive for reconnection.
func (s *LiveSession[S]) run() {
	dev.Debug("run loop started", "session", s.id, "endpoint", s.endpoint)
	s.loopRunning.Store(true)
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
				continue
			}
			s.exec(ev)

		case cmd := <-s.cmds:
			s.runCmd(cmd)

		case fn := <-s.fxCh:
			// Effect arriving outside of Handle — send immediately.
			fx := &effects{}
			fn(fx)
			s.sendFx(fx)

		case <-s.ctx.Done():
			return
		}
	}
}

// runCmd executes a command with panic recovery so a misbehaving
// command cannot crash the entire process.
func (s *LiveSession[S]) runCmd(cmd func()) {
	defer func() {
		if r := recover(); r != nil {
			err := panicErr(r)
			slog.Error("panic in command", "session", s.id, "panic", r)
			s.emitDiagnostic(Diagnostic{
				Kind:      HandlerPanic,
				SessionID: s.id,
				Err:       err,
				Detail:    s.endpoint,
			})
		}
	}()
	cmd()
}

// readTransport bridges the blocking ReceiveEvent call into the
// events channel. Its only job is to read and forward — it closes
// the output channel on exit so the loop knows the transport is gone.
func (s *LiveSession[S]) readTransport(out chan<- Event) {
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
func (s *LiveSession[S]) exec(ev Event) {
	now := time.Now()
	s.lastActivity.Store(now.UnixNano())
	if s.idleTimer != nil {
		s.idleTimer.Reset(s.idleTimeout)
	}

	// Snapshot the state before entering Handle so that concurrent
	// callers of State() can read it safely via the atomic Value.
	// The snapshot is stored before handling is set to true —
	// sequential consistency of atomics guarantees any goroutine
	// that observes handling=true also sees the snapshot.
	s.stateSnap.Store(s.state)
	s.handling.Store(true)
	fx := &effects{}
	defer func() {
		if r := recover(); r != nil {
			err := panicErr(r)
			slog.Error("panic in handler", "session", s.id, "action", ev.Action, "panic", r)
			s.emitDiagnostic(Diagnostic{
				Kind:      HandlerPanic,
				SessionID: s.id,
				Err:       err,
				Detail:    ev.Action,
			})
			s.drainFx(nil)
		}
		s.handling.Store(false)
	}()

	dev.Debug("event received",
		"session", s.id,
		"endpoint", s.endpoint,
		"action", ev.Action,
		"type", ev.Type,
	)

	// Component mounts intercept events before Handle so that
	// mounted components are self-contained — the application's
	// Handle never sees events meant for a component. Navigate
	// events bypass mounts because they need the OnNavigate chain.
	var newState S
	handled := false
	if ev.Type != "navigate" {
		newState, handled = RouteMount(s.mounts, s, s.state, ev)
	}
	if !handled {
		newState = s.handle(s, s.state, ev)
	}

	s.drainFx(fx)

	if s.equal != nil && s.equal(s.state, newState) {
		dev.Debug("state unchanged, skipping render",
			"session", s.id,
			"action", ev.Action,
		)
		if fx.any() || ev.EventID != "" {
			u := wire.Update{EventID: ev.EventID}
			fx.merge(&u)
			s.send(u)
		}
		return
	}
	s.state = newState

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

// onTransportClose handles transport disconnection. The transport is
// closed and nilled so send() silently drops updates during the
// reconnect window. A disconnect timer is started (if reconnection is
// enabled) and the onDisconnect callback fires for pool transitions.
func (s *LiveSession[S]) onTransportClose() {
	dev.Debug("transport closed",
		"session", s.id,
		"endpoint", s.endpoint,
		"url", s.lastURL,
	)
	s.transport.Close()
	s.transport = nil

	if s.reconnectTimeout > 0 {
		dev.Debug("disconnect timer started",
			"session", s.id,
			"endpoint", s.endpoint,
			"timeout", s.reconnectTimeout,
		)
		s.disconnectTimer = time.AfterFunc(s.reconnectTimeout, func() {
			s.stop()
		})
	}

	if s.onDisconnect != nil {
		s.onDisconnect()
	}
}

// cleanup runs when the loop exits. It stops lifecycle timers so
// their callbacks don't fire on a dead session.
func (s *LiveSession[S]) cleanup() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
	}
	if s.transport != nil {
		s.transport.Close()
	}
}

// sendDiff is the render-diff-send pipeline extracted from exec and
// Update. It handles patches, structural changes, and the no-diff
// case where only the eventID needs echoing. fx carries any buffered
// effects to merge into the update message.
func (s *LiveSession[S]) sendDiff(eventID string, patches []jit.Patch, change *jit.StructuralChange, tree node.Node, fx *effects) {
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

	if len(patches) > 0 || (fx != nil && fx.any()) {
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
func (s *LiveSession[S]) send(u wire.Update) {
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
func (s *LiveSession[S]) startTimers() {
	if s.idleTimeout > 0 {
		s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
			s.stop()
		})
	}
}
