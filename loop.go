package poly

import (
	"errors"
	"io"
	"net/url"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent-poly/event"
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
func (s *Session[S]) run() {
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
func (s *Session[S]) runCmd(cmd func()) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in command", "panic", r)
		}
	}()
	cmd()
}

// readTransport bridges the blocking ReceiveEvent call into the
// events channel. Its only job is to read and forward — it closes
// the output channel on exit so the loop knows the transport is gone.
func (s *Session[S]) readTransport(out chan<- Event) {
	defer close(out)
	for {
		ev, err := s.transport.ReceiveEvent()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger.Error("receive error", "err", err)
			}
			return
		}
		out <- ev
	}
}

// exec is the core pipeline. Every client event passes through it:
// activity tracking → snapshot → handle → drain effects → state
// check → render → diff → send.
func (s *Session[S]) exec(ev Event) {
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
			s.logger.Error("panic in handler",
				"action", ev.Action,
				"panic", r,
			)
			// Discard any effects enqueued during the panicked handler.
			s.drainFx(nil)
		}
		s.handling.Store(false)
	}()

	// Phase 1: Handle — produce new state.
	var newState S
	if ev.Type == event.Navigate && s.onNavigate != nil {
		params := Params{Path: ev.Data["path"]}
		if search := ev.Data["search"]; search != "" {
			params.Query, _ = url.ParseQuery(search)
		}
		newState = s.onNavigate(s, s.state, params)
	} else {
		newState = s.handle(s, s.state, ev)
	}

	// Phase 2: Collect effects enqueued during Handle.
	s.drainFx(fx)

	// Phase 3: State check — skip render if unchanged.
	if s.equal != nil && s.equal(s.state, newState) {
		if fx.any() || ev.EventID != "" {
			u := update{EventID: ev.EventID}
			fx.merge(&u)
			s.send(u)
		}
		return
	}
	s.state = newState

	// Phase 4: Render + Diff.
	tree := s.render(s.state)
	patches, change := s.differ.Diff(tree)

	// Phase 5: Build and send update.
	s.sendDiff(ev.EventID, patches, change, tree, fx)
}

// onTransportClose handles transport disconnection. The transport is
// closed, a disconnect timer is started (if reconnection is enabled),
// and the onDisconnect callback fires for pool transitions.
func (s *Session[S]) onTransportClose() {
	s.transport.Close()

	if s.reconnectTimeout > 0 {
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
func (s *Session[S]) cleanup() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
	}
	s.transport.Close()
}

// sendDiff is the render-diff-send pipeline extracted from exec and
// Update. It handles patches, structural changes, and the no-diff
// case where only the eventID needs echoing. fx carries any buffered
// effects to merge into the update message.
func (s *Session[S]) sendDiff(eventID string, patches []jit.Patch, change *jit.StructuralChange, tree node.Node, fx *effects) {
	if change != nil {
		html := s.differ.Render(tree)
		s.logger.Warn("structural change, sending root morph",
			"change", change.String(),
			"bytes", len(html),
			"tip", "wrap conditional elements in a keyed container to scope this morph",
		)

		if s.onStructuralChange != nil {
			s.onStructuralChange(s, StructuralChange{
				Added:     change.Added,
				Removed:   change.Removed,
				Reordered: change.Reordered,
				Bytes:     len(html),
			})
		}

		u := update{
			Morphs:  []morph{{Key: "", HTML: html}},
			EventID: eventID,
		}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

	if len(patches) > 0 || (fx != nil && fx.any()) {
		u := update{Patches: patches, EventID: eventID}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

	// No patches and no structural change. Still echo the eventID
	// so the client can restore any loading state.
	if eventID != "" {
		s.send(update{EventID: eventID})
	}
}

// send encodes an update as JSON and writes the bytes to the
// transport. URL and title are captured before the nil-transport
// guard so reattach can replay them after a disconnect.
func (s *Session[S]) send(u update) {
	if u.URL != "" {
		s.lastURL = u.URL
	}
	if u.Title != "" {
		s.lastTitle = u.Title
	}
	if s.transport == nil {
		return
	}
	data, err := marshalUpdate(u)
	if err != nil {
		s.logger.Error("encode update error", "err", err)
		return
	}
	if err := s.transport.Send(data); err != nil {
		s.logger.Error("send update error", "err", err)
	}
}

// startTimers sets up per-session lifecycle timers. Called once during
// session creation in serve.go. Timer cleanup happens in [cleanup],
// which runs as a defer in [run] when the loop exits.
func (s *Session[S]) startTimers() {
	if s.idleTimeout > 0 {
		s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
			s.stop()
		})
	}
}
