package poly

import (
	"context"
	"errors"
	"io"
	"net/url"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
)

// run is the session's command loop. It processes transport events and
// commands from external callers in a single goroutine — no mutex
// needed. The loop exits when the session context is cancelled
// (shutdown, reaper timeout, or explicit destruction).
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
			now := time.Now()
			s.lastActivity.Store(now.UnixNano())
			if s.idleTimer != nil {
				s.idleTimer.Reset(s.idleTimeout)
			}
			s.exec(ev)

		case cmd := <-s.cmds:
			cmd()

		case <-s.ctx.Done():
			return
		}
	}
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
// handle → state check → render → diff → flush effects → send.
func (s *Session[S]) exec(ev Event) {
	s.handling = true
	s.fx = &effects{}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in handler",
				"action", ev.Action,
				"panic", r,
			)
			// Discard buffered emissions — the handler's state
			// change is incomplete so its events are invalid.
			s.emitted = s.emitted[:0]
		}
		s.handling = false
		s.fx = nil
	}()

	// Phase 1: Handle — produce new state, accumulate effects.
	var newState S
	if ev.Type == "navigate" && s.handleParams != nil {
		params := Params{Path: ev.Data["path"]}
		if search := ev.Data["search"]; search != "" {
			params.Query, _ = url.ParseQuery(search)
		}
		newState = s.handleParams(s, s.state, params)
	} else {
		newState = s.handle(s, s.state, ev)
	}

	// Phase 2: State check — skip render if unchanged.
	if s.equal != nil && s.equal(s.state, newState) {
		if s.fx.any() || ev.EventID != "" {
			u := update{EventID: ev.EventID}
			s.fx.merge(&u)
			s.send(u)
		}
		s.flushEmissions()
		return
	}
	s.state = newState

	// Phase 3: Render + Diff.
	tree := s.render(s.state)
	patches, change := s.differ.Diff(tree)

	// Phase 4: Build and send update.
	s.sendDiff(ev.EventID, patches, change, tree)

	// Phase 5: Publish buffered domain events.
	s.flushEmissions()
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
// case where only the eventID needs echoing.
func (s *Session[S]) sendDiff(eventID string, patches []jit.Patch, change *jit.StructuralChange, tree node.Node) {
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
		if s.fx != nil {
			s.fx.merge(&u)
		}
		s.send(u)
		return
	}

	if len(patches) > 0 || (s.fx != nil && s.fx.any()) {
		u := update{Patches: patches, EventID: eventID}
		if s.fx != nil {
			s.fx.merge(&u)
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
// session creation in lifecycle.go. context.AfterFunc ensures timers
// are stopped when the session is destroyed even if cleanup hasn't run.
func (s *Session[S]) startTimers() {
	if s.idleTimeout > 0 {
		s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
			s.stop()
		})
	}

	// context.AfterFunc fires when the session context is cancelled,
	// stopping any running timers to prevent callbacks on dead sessions.
	context.AfterFunc(s.ctx, func() {
		if s.idleTimer != nil {
			s.idleTimer.Stop()
		}
		if s.disconnectTimer != nil {
			s.disconnectTimer.Stop()
		}
	})
}
