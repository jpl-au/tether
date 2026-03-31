package tether

import (
	"errors"
	"io"
	"time"

	"github.com/jpl-au/tether/dev"
)

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
			// Drain additional pending commands to coalesce Updates.
			// Multiple rapid Updates (broadcasts, watchers) execute
			// their mutations sequentially but share a single
			// render-diff-send cycle. Bounded by current channel
			// length so ctx.Done is checked on the next iteration.
			batched := 1
			for range len(s.cmds) {
				if s.ctx.Err() != nil {
					break
				}
				select {
				case cmd := <-s.cmds:
					s.runCmd(cmd)
					batched++
				default:
				}
			}
			if s.needsRender && s.ctx.Err() == nil {
				s.needsRender = false
				s.coalescedCount = batched
				s.coalescedRender()
			}

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
			switch {
			case errors.Is(err, io.EOF):
				dev.Debug("transport EOF (normal close)",
					"session", s.id,
					"endpoint", s.endpoint,
				)
			default:
				dev.Debug("transport error",
					"session", s.id,
					"endpoint", s.endpoint,
					"error", err,
				)
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
