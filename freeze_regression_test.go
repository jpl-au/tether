package tether

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
)

func TestFreezeThawSubscriptionLifetime(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "Watchers", true: "OnRestore"}[manual], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bus, value := NewBus[int](), NewValue(0)
				subscribe := func(s *StatefulSession[counterState]) {
					On(s, bus, func(n int, c counterState) counterState { c.Count += n; return c })
					Observe(s, value, func(n int, c counterState) counterState { c.Count += n; return c })
				}
				h := newThawHandler(newSessionFileStore(t))
				if manual {
					h.cfg.OnRestore = subscribe
				} else {
					h.cfg.Watchers = []Watcher[counterState]{
						WatchBus(bus, func(n int, c counterState) counterState { c.Count += n; return c }),
						WatchValue(value, func(n int, c counterState) counterState { c.Count += n; return c }),
					}
				}
				ct := newConnectedTransport()
				s := newTestSession(counterState{}, ct)
				s.handler, s.freeze, s.sessionStore, s.codec = h, true, h.cfg.SessionStore, cborCodec[counterState]{}
				s.reconnectTimeout = h.cfg.Timeouts.Reconnect
				h.active[s.id] = s
				go s.run()
				go s.readTransport(s.events)
				subscribe(s)
				synctest.Wait()
				for cycle := range 3 {
					ct.Close()
					synctest.Wait()
					if bus.Len() != 0 {
						t.Errorf("cycle %d: subscriptions survived freeze: %d", cycle, bus.Len())
					}
					if s.Context().Err() != nil {
						t.Fatal("freeze cancelled the session lifetime")
					}
					ct = newConnectedTransport()
					h.mu.Lock()
					delete(h.disconnected, s.id)
					h.active[s.id] = s
					h.mu.Unlock()
					go h.thaw(s, httptest.NewRequest("GET", "/", nil), ct)
					synctest.Wait()
					before := s.State().Count
					bus.Publish(1)
					synctest.Wait()
					if s.State().Count != before+1 || bus.Len() != 1 {
						t.Errorf("cycle %d: duplicate delivery: before=%d after=%d subscriptions=%d", cycle, before, s.State().Count, bus.Len())
					}
					value.Store(1)
					synctest.Wait()
					if s.State().Count != before+2 {
						t.Errorf("cycle %d: duplicate observation: Count=%d want %d", cycle, s.State().Count, before+2)
					}
					value.Store(0)
					synctest.Wait()
				}
				if err := h.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
			})
		})
	}
}

func TestThawSendsInitialCatchup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newThawHandler(newSessionFileStore(t))
		s := freezeSession(t, h, counterState{Count: 42}, nil)
		ct := newConnectedTransport()
		h.mu.Lock()
		delete(h.disconnected, s.id)
		h.active[s.id] = s
		h.mu.Unlock()
		go h.thaw(s, httptest.NewRequest("GET", "/", nil), ct)
		synctest.Wait()
		ct.mu.Lock()
		if len(ct.sent) != 1 {
			t.Errorf("catch-up messages=%d, want 1", len(ct.sent))
		} else {
			msg := decodeMessage(ct.sent[0])
			if len(msg.Morphs) != 1 || !strings.Contains(msg.Morphs[0].HTML, "Count: 42") {
				t.Errorf("missing restored DOM: %+v", msg)
			}
		}
		ct.mu.Unlock()
		h.Shutdown(context.Background())
		synctest.Wait()
	})
}

func TestReconnectWaitsForFreezeCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		h := newThawHandler(newSessionFileStore(t), func(c *StatefulConfig[counterState]) {
			c.OnDisconnect = func(*StatefulSession[counterState]) { close(entered); <-release }
		})
		h.app.Logger = slog.Default()
		ct := newConnectedTransport()
		s := newTestSession(counterState{Count: 42}, ct)
		s.id, s.handler, s.freeze, s.sessionStore, s.codec = newID(), h, true, h.cfg.SessionStore, cborCodec[counterState]{}
		s.reconnectTimeout = h.cfg.Timeouts.Reconnect
		h.active[s.id] = s
		go s.run()
		go s.readTransport(s.events)
		synctest.Wait()
		ct.Close()
		<-entered
		next := newConnectedTransport()
		go h.serveSession(httptest.NewRecorder(), httptest.NewRequest("GET", connectPath(h, s.id, ""), nil), func(http.ResponseWriter, *http.Request) (Transport, error) { return next, nil })
		synctest.Wait()
		close(release)
		synctest.Wait()
		if Status(s.status.Load()) != Active || s.State().Count != 42 {
			t.Errorf("reconnect stranded session: status=%s state=%+v", Status(s.status.Load()), s.State())
		}
		h.Shutdown(context.Background())
		next.Close()
		synctest.Wait()
	})
}
