package tether

import (
	"context"
	"net/http/httptest"
	"testing"
	"testing/synctest"
)

func TestThawRetiresOldOverflowQueue(t *testing.T) {
	for _, effect := range []bool{false, true} {
		t.Run(map[bool]string{false: "command", true: "effect"}[effect], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newThawHandler(newSessionFileStore(t))
				s := newTestSession(counterState{Count: 42}, newConnectedTransport())
				s.handler, s.sessionStore, s.codec, s.freeze = h, h.cfg.SessionStore, cborCodec[counterState]{}, true
				s.status.Store(int32(Active))
				s.cmds = make(chan func(), 1)
				s.fxCh = make(chan func(*Effects), 1)
				s.overflowSem = make(chan struct{}, 1)
				oldSem := s.overflowSem
				entered, release := make(chan struct{}), make(chan struct{})
				s.diagnostics = NewBus[Diagnostic]()
				s.diagnostics.Subscribe(context.Background(), func(d Diagnostic) {
					if d.Kind == CommandDiscarded {
						close(entered)
						<-release
					}
				})
				if effect {
					s.Toast("old")
					s.Toast("overflow")
				} else {
					s.Update(func(c counterState) counterState { c.Count = 99; return c })
					s.Update(func(c counterState) counterState { c.Count = 100; return c })
				}
				synctest.Wait()
				// Reproduce a frozen loop with an overflow goroutine still
				// finishing its discard diagnostic before returning its slot.
				s.saveSessionState(context.Background(), h.cfg.Timeouts.Reconnect)
				s.status.Store(int32(Frozen))
				s.stateReleased = true
				close(s.loopDone)
				<-entered
				h.active[s.id] = s
				go h.thaw(s, httptest.NewRequest("GET", "/", nil), newConnectedTransport())
				synctest.Wait()
				close(release)
				synctest.Wait()
				if len(oldSem) != 0 {
					t.Error("old overflow goroutine tried to release the new runtime's semaphore")
					// Unblock the broken implementation so the regression can
					// report normally instead of leaking its goroutine.
					s.overflowSem <- struct{}{}
				}
				if s.State().Count != 42 {
					t.Errorf("old mutation replayed after thaw: %+v", s.State())
				}
				h.Shutdown(context.Background())
				synctest.Wait()
			})
		})
	}
}
