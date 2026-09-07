package tether

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
)

func TestCapacityReservedDuringInitialisation(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "MaxSessions", true: "MaxPending"}[pending], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newReattachHandler()
				if pending {
					h.app.MaxPending = 1
				} else {
					h.app.MaxSessions = 1
				}
				entered, release := make(chan struct{}, 2), make(chan struct{})
				h.cfg.InitialState = func(*http.Request) counterState { entered <- struct{}{}; <-release; return counterState{} }
				first, second := httptest.NewRecorder(), httptest.NewRecorder()
				go h.serveInitialPage(first, httptest.NewRequest("GET", "/", nil))
				<-entered
				go h.serveInitialPage(second, httptest.NewRequest("GET", "/", nil))
				synctest.Wait()
				if second.Code != http.StatusServiceUnavailable {
					t.Errorf("concurrent request was not rejected: status=%d", second.Code)
				}
				close(release)
				synctest.Wait()
				if len(h.pending) != 1 {
					t.Errorf("reserved capacity exceeded: pending=%d", len(h.pending))
				}
				h.Shutdown(context.Background())
			})
		})
	}
}

func TestCapacityReservedForDirectConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		h.app.Logger, h.app.MaxSessions = slog.Default(), 1
		entered, release := make(chan struct{}, 2), make(chan struct{})
		h.cfg.InitialState = func(*http.Request) counterState { entered <- struct{}{}; <-release; return counterState{} }
		ct := newConnectedTransport()
		go h.serveSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), func(http.ResponseWriter, *http.Request) (Transport, error) { return ct, nil })
		<-entered
		second := httptest.NewRecorder()
		go h.serveInitialPage(second, httptest.NewRequest("GET", "/", nil))
		synctest.Wait()
		if second.Code != http.StatusServiceUnavailable {
			t.Errorf("direct connect did not reserve capacity: status=%d", second.Code)
		}
		close(release)
		synctest.Wait()
		h.Shutdown(context.Background())
		synctest.Wait()
	})
}

func TestCapacityReleasedAfterInitialisationFailure(t *testing.T) {
	h := newReattachHandler()
	h.app.MaxPending, h.app.MaxSessions = 1, 1
	h.cfg.InitialState = func(*http.Request) counterState { panic("initialisation failed") }
	h.serveInitialPage(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	h.cfg.InitialState = func(*http.Request) counterState { return counterState{} }
	w := httptest.NewRecorder()
	h.serveInitialPage(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK || len(h.pending) != 1 {
		t.Errorf("capacity leaked: status=%d pending=%d", w.Code, len(h.pending))
	}
}
