package tether

import (
	"context"
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
	s.queue()
	dev.Debug("run loop started", "session", s.id, "endpoint", s.endpoint)
	s.stateSnap.Store(s.state)
	// Fresh sessions activate here. Thawed sessions were already
	// moved Frozen → Active by thaw (atomically, so a concurrent
	// Shutdown cannot destroy the frozen stub mid-thaw), and a
	// session that lost that race to Shutdown is Destroyed - the
	// loop starts, sees the cancelled context, and exits cleanly.
	if Status(s.status.Load()) == Pending {
		s.transition(Active)
	}
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
				if Status(s.status.Load()) == Frozen {
					// Frozen - exit the loop. State has been
					// persisted and memory released. A reconnect
					// will thaw by starting a new run().
					return
				}
				continue
			}
			s.exec(ev)

		case cmd := <-s.cmds:
			// Snapshot the pre-batch state so the optional Equal
			// check can skip the render when the whole batch left
			// state unchanged - matching the event path in exec.
			var prev S
			if s.equal != nil {
				prev = s.state
			}
			s.batchHasDOMUpdate = false
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
				if s.equal != nil && !s.batchHasDOMUpdate && s.equal(prev, s.state) {
					// State and DOM unchanged across the batch - skip
					// the render but still deliver buffered effects.
					fx := &Effects{}
					s.drainFx(fx)
					s.sendFx(fx)
				} else {
					s.coalescedRender()
				}
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
	ctx := s.Context()
	if p := s.transportCtx.Load(); p != nil {
		ctx = *p
	}
	s.readAttachment(out, s.transport, ctx)
}

// startReader captures an attachment before launching its goroutine. A
// retiring reader must never follow the session's replacement transport.
func (s *StatefulSession[S]) startReader() <-chan struct{} {
	return s.prepareReader()()
}

// prepareReader captures before the loop starts. Startup callbacks may
// delay reading while navigation on the loop temporarily detaches transport.
func (s *StatefulSession[S]) prepareReader() func() <-chan struct{} {
	tr, out, ctx := s.transport, s.events, *s.transportCtx.Load()
	return func() <-chan struct{} {
		done := make(chan struct{})
		go func() { defer close(done); s.readAttachment(out, tr, ctx) }()
		return done
	}
}

func (s *StatefulSession[S]) readAttachment(out chan<- Event, tr Transport, ctx context.Context) {
	dev.Debug("readTransport started", "session", s.id, "endpoint", s.endpoint)
	defer close(out)
	for {
		ev, err := tr.ReceiveEvent()
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
		// Select on both the send and the transport context so the
		// goroutine exits promptly when the session is destroyed,
		// instead of blocking forever on a full channel.
		select {
		case out <- ev:
		case <-ctx.Done():
			return
		}
	}
}
