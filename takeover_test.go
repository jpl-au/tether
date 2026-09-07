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

func TestActiveTransportTakeoverPreservesSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		h.app.Logger = slog.Default()
		h.cfg.InitialState = func(*http.Request) counterState { return counterState{} }
		first := newConnectedTransport()
		s := newTestSession(counterState{Count: 42}, first)
		s.id, s.handler = newID(), h
		h.active[s.id] = s
		go s.run()
		go s.readTransport(s.events)
		synctest.Wait()
		originalCtx := *s.transportCtx.Load()
		for range 3 {
			next := newConnectedTransport()
			go h.serveSession(httptest.NewRecorder(), httptest.NewRequest("GET", connectPath(h, s.id, ""), nil), func(http.ResponseWriter, *http.Request) (Transport, error) { return next, nil })
			synctest.Wait()
			h.mu.RLock()
			if len(h.active) != 1 || h.active[s.id] != s {
				t.Errorf("takeover created another session: active=%d", len(h.active))
			}
			h.mu.RUnlock()
			if s.State().Count != 42 || s.transport != next {
				t.Error("existing session was not reattached")
			}
			next.mu.Lock()
			if len(next.sent) == 0 {
				t.Error("takeover sent no catch-up")
			} else {
				msg := decodeMessage(next.sent[0])
				if len(msg.Morphs) != 1 || !strings.Contains(msg.Morphs[0].HTML, "Count: 42") {
					t.Errorf("incorrect takeover DOM: %+v", msg)
				}
			}
			next.mu.Unlock()
		}
		if originalCtx.Err() == nil {
			t.Error("old attachment context survived takeover")
		}
		first.mu.Lock()
		closed := first.closed
		first.mu.Unlock()
		if !closed {
			t.Error("old transport survived takeover")
		}
		h.Shutdown(context.Background())
		synctest.Wait()
	})
}
