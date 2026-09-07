package tether

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type freezeSaveStore struct {
	SessionStore
	entered chan struct{}
	release chan struct{}
	fail    bool
}

func (s freezeSaveStore) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	if s.fail {
		return errors.New("store unavailable")
	}
	return s.SessionStore.Save(ctx, id, data, ttl)
}

func TestFreezeKeepsStateWhenSaveFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := freezeSaveStore{SessionStore: newSessionFileStore(t), fail: true}
		h := newThawHandler(store)
		ct := newConnectedTransport()
		s := newTestSession(counterState{Count: 42}, ct)
		s.handler, s.freeze, s.sessionStore, s.codec = h, true, store, cborCodec[counterState]{}
		h.active[s.id] = s
		go s.run()
		go s.readTransport(s.events)
		synctest.Wait()
		ct.Close()
		synctest.Wait()
		if Status(s.status.Load()) != Active || s.State().Count != 42 {
			t.Errorf("failed persistence released live state: status=%s state=%+v", Status(s.status.Load()), s.State())
		}
		s.Update(func(c counterState) counterState { c.Count++; return c })
		synctest.Wait()
		if s.State().Count != 43 {
			t.Error("session cannot continue after failed freeze")
		}
		s.stop()
		synctest.Wait()
	})
}

func TestPostDuringFreezeReturnsRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		store := freezeSaveStore{SessionStore: newSessionFileStore(t), entered: entered, release: release}
		h := newThawHandler(store)
		h.csrf = &http.CrossOriginProtection{}
		h.cfg.Limits.MaxEventBytes = 4096
		ct := newConnectedTransport()
		s := newTestSession(counterState{Count: 42}, ct)
		s.id, s.handler, s.freeze, s.sessionStore, s.codec = newID(), h, true, store, cborCodec[counterState]{}
		h.active[s.id] = s
		go s.run()
		go s.readTransport(s.events)
		synctest.Wait()
		ct.Close()
		<-entered
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"type":"click","action":"increment"}`))
		r.Header.Set("Tether-Session", s.id)
		w := httptest.NewRecorder()
		h.handlePostEvent(w, r)
		if w.Code != 503 || w.Header().Get("Retry-After") == "" {
			t.Errorf("event accepted while persistence was in progress: status=%d", w.Code)
		}
		close(release)
		synctest.Wait()
		s.stop()
		synctest.Wait()
	})
}

type freezeCloseGate struct {
	*connectedTransport
	entered, release chan struct{}
}

func (t *freezeCloseGate) Close() error {
	close(t.entered)
	<-t.release
	return t.connectedTransport.Close()
}

func TestFreezePersistsAlreadyAcceptedPost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newThawHandler(newSessionFileStore(t))
		h.csrf = &http.CrossOriginProtection{}
		h.cfg.Limits.MaxEventBytes = 4096
		ct := &freezeCloseGate{newConnectedTransport(), make(chan struct{}), make(chan struct{})}
		s := newTestSession(counterState{Count: 42}, ct)
		s.id, s.handler, s.freeze, s.sessionStore, s.codec = newID(), h, true, h.cfg.SessionStore, cborCodec[counterState]{}
		h.active[s.id] = s
		go s.run()
		go s.readTransport(s.events)
		synctest.Wait()
		ct.connectedTransport.Close()
		<-ct.entered
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"type":"click","action":"increment"}`))
		r.Header.Set("Tether-Session", s.id)
		w := httptest.NewRecorder()
		h.handlePostEvent(w, r)
		if w.Code != 204 {
			t.Fatalf("event not accepted before freeze: %d", w.Code)
		}
		close(ct.release)
		synctest.Wait()
		assertStoredCount(t, h.cfg.SessionStore, s.id, 43)
		s.stop()
		synctest.Wait()
	})
}
