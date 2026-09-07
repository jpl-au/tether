package tether

import (
	"context"
	"net/http/httptest"
	"testing"
	"testing/synctest"

	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/event"
)

func TestRestoreRendersCatchupOnce(t *testing.T) {
	for _, thaw := range []bool{false, true} {
		for _, memo := range []bool{false, true} {
			t.Run(map[bool]string{false: "restore", true: "thaw"}[thaw]+map[bool]string{false: "Differ", true: "Memoiser"}[memo], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					h := newThawHandler(newSessionFileStore(t))
					s := freezeSession(t, h, counterState{Count: 42}, nil)
					renders := 0
					h.cfg.Memoise = memo
					h.cfg.Render = func(c counterState) node.Node { renders++; return renderCounter(c) }
					s.render = h.cfg.Render
					ct := newConnectedTransport()
					if thaw {
						h.mu.Lock()
						delete(h.disconnected, s.id)
						h.active[s.id] = s
						h.mu.Unlock()
						go h.thaw(s, httptest.NewRequest("GET", "/", nil), ct)
					} else {
						h.mu.Lock()
						delete(h.disconnected, s.id)
						h.mu.Unlock()
						go h.restoreSession(s.id, httptest.NewRequest("GET", "/", nil), ct)
					}
					synctest.Wait()
					if renders != 1 {
						t.Errorf("restoring unchanged state rendered %d times, want one delivered render", renders)
					}
					h.Shutdown(context.Background())
					s.stop()
					synctest.Wait()
				})
			})
		}
	}
}

func TestRestoreStoppedDuringCallbackDoesNotRejoinGroups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := NewGroup[counterState]()
		h := newThawHandler(newSessionFileStore(t), func(c *StatefulConfig[counterState]) { c.Groups = []*Group[counterState]{g} })
		s := freezeSession(t, h, counterState{Count: 42}, nil)
		h.cfg.OnRestore = func(s *StatefulSession[counterState]) { s.stop(); <-s.loopDone }
		go h.thaw(s, httptest.NewRequest("GET", "/", nil), newConnectedTransport())
		synctest.Wait()
		if g.Len() != 0 {
			t.Error("restore rejoined groups after destruction")
		}
		h.Shutdown(context.Background())
	})
}

func TestShutdownDuringRestoreCallbackKeepsRecoveryState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)
		s := freezeSession(t, h, counterState{Count: 42}, nil)
		entered, release := make(chan struct{}), make(chan struct{})
		h.cfg.OnRestore = func(*StatefulSession[counterState]) { close(entered); <-release }
		go h.thaw(s, httptest.NewRequest("GET", "/", nil), newConnectedTransport())
		<-entered
		h.Shutdown(context.Background())
		close(release)
		synctest.Wait()
		assertStoredCount(t, store, s.id, 42)
	})
}

func TestRestoreReaderKeepsAttachmentDuringNavigation(t *testing.T) {
	for _, thaw := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "thaw"}[thaw], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newThawHandler(newSessionFileStore(t))
				s := freezeSession(t, h, counterState{Count: 42}, nil)
				entered, release := make(chan struct{}), make(chan struct{})
				h.cfg.Handle = func(_ Session, c counterState, ev Event) counterState {
					if ev.Type == event.Navigate {
						close(entered)
						<-release
					}
					return c
				}
				s.handle = h.cfg.Handle
				h.cfg.OnRestore = func(*StatefulSession[counterState]) { <-entered }
				ct := newConnectedTransport()
				if thaw {
					go h.thaw(s, httptest.NewRequest("GET", "/", nil), ct, "/offline")
				} else {
					delete(h.disconnected, s.id)
					go h.restoreSession(s.id, httptest.NewRequest("GET", "/", nil), ct, "/offline")
				}
				<-entered
				synctest.Wait()
				close(release)
				synctest.Wait()
				h.Shutdown(context.Background())
				s.stop()
				synctest.Wait()
			})
		})
	}
}

func TestThawDeletesObsoleteDiffSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		diffs := newFileStore(t)
		h := newThawHandler(newSessionFileStore(t), func(c *StatefulConfig[counterState]) { c.DiffStore = diffs })
		s := freezeSession(t, h, counterState{Count: 42}, nil)
		if err := diffs.Save(context.Background(), s.id, []byte("old baseline")); err != nil {
			t.Fatal(err)
		}
		ct := newConnectedTransport()
		go h.thaw(s, httptest.NewRequest("GET", "/", nil), ct)
		synctest.Wait()
		if data, err := diffs.Load(context.Background(), s.id); err != nil || data != nil {
			t.Errorf("obsolete snapshot survived thaw: %q, %v", data, err)
		}
		h.Shutdown(context.Background())
		synctest.Wait()
	})
}

func TestThawRenderPanicDestroysSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newThawHandler(newSessionFileStore(t))
		s := freezeSession(t, h, counterState{Count: 42}, nil)
		h.cfg.Render = func(counterState) node.Node { panic("render failed") }
		s.render = h.cfg.Render
		ct := newConnectedTransport()
		h.mu.Lock()
		delete(h.disconnected, s.id)
		h.active[s.id] = s
		h.mu.Unlock()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("thaw leaked a panic: %v", r)
				}
			}()
			h.thaw(s, httptest.NewRequest("GET", "/", nil), ct)
		}()
		synctest.Wait()
		if s.Context().Err() == nil {
			t.Error("failed thaw left a live session")
		}
		ct.mu.Lock()
		closed := ct.closed
		ct.mu.Unlock()
		if !closed {
			t.Error("failed thaw left its transport open")
		}
		s.stop()
		ct.Close()
		synctest.Wait()
	})
}
