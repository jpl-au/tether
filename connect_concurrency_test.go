package tether

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"
)

type restoreLoadGate struct {
	SessionStore
	entered chan struct{}
	release chan struct{}
}

func TestFrozenMaxLifetimeReleasesPool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newThawHandler(newSessionFileStore(t))
		h.app.Logger = slog.Default()
		h.cfg.InitialState = func(*http.Request) counterState { return counterState{Count: 42} }
		h.cfg.Timeouts.MaxLifetime = 200 * time.Millisecond
		var sess *StatefulSession[counterState]
		h.cfg.OnConnect = func(s *StatefulSession[counterState]) { sess = s }
		ct := newConnectedTransport()
		go h.serveSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), func(http.ResponseWriter, *http.Request) (Transport, error) { return ct, nil })
		synctest.Wait()
		ct.Close()
		synctest.Wait()
		if Status(sess.status.Load()) != Frozen {
			t.Fatal("session did not freeze")
		}
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()
		if len(h.disconnected) != 0 || Status(sess.status.Load()) != Destroyed {
			t.Error("max lifetime left a frozen session in the pool")
		}
		h.Shutdown(context.Background())
	})
}

func (s restoreLoadGate) Load(ctx context.Context, id string) ([]byte, error) {
	data, err := s.SessionStore.Load(ctx, id)
	s.entered <- struct{}{}
	<-s.release
	return data, err
}

func TestConcurrentSessionRecovery(t *testing.T) {
	for _, thaw := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "thaw"}[thaw], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := newSessionFileStore(t)
				h := newThawHandler(store)
				h.app.Logger = slog.Default()
				s := freezeSession(t, h, counterState{Count: 42}, nil)
				if !thaw {
					delete(h.disconnected, s.id)
				}
				gate := restoreLoadGate{store, make(chan struct{}, 2), make(chan struct{})}
				h.cfg.SessionStore = gate
				var restored []*StatefulSession[counterState]
				h.cfg.OnRestore = func(s *StatefulSession[counterState]) { restored = append(restored, s) }
				first, second := newConnectedTransport(), newConnectedTransport()
				connect := func(ct Transport) {
					r := httptest.NewRequest("GET", connectPath(h, s.id, ""), nil)
					h.serveSession(httptest.NewRecorder(), r, func(http.ResponseWriter, *http.Request) (Transport, error) { return ct, nil })
				}
				go connect(first)
				<-gate.entered
				go connect(second)
				synctest.Wait()
				if len(gate.entered) != 0 {
					t.Error("overlapping request started a second restore")
				}
				close(gate.release)
				synctest.Wait()
				if len(restored) != 1 || len(h.active) != 1 {
					t.Errorf("recovery was not exclusive: callbacks=%d active=%d", len(restored), len(h.active))
				}
				for _, live := range restored {
					live.stop()
				}
				s.stop()
				first.Close()
				second.Close()
				synctest.Wait()
				h.Shutdown(context.Background())
			})
		})
	}
}

func TestShutdownDuringRestore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)
		s := freezeSession(t, h, counterState{Count: 42}, nil)
		delete(h.disconnected, s.id)
		gate := restoreLoadGate{store, make(chan struct{}, 1), make(chan struct{})}
		h.cfg.SessionStore = gate
		ct := newConnectedTransport()
		go h.restoreSession(s.id, httptest.NewRequest("GET", "/", nil), ct)
		<-gate.entered
		h.Shutdown(context.Background())
		close(gate.release)
		synctest.Wait()
		if len(h.active) != 0 {
			t.Error("restore repopulated a shut down handler")
		}
		for _, live := range h.active {
			live.stop()
		}
		s.stop()
		ct.Close()
		synctest.Wait()
	})
}
