package tether

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"

	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/mode"
)

func TestSessionHandlePanicDoesNotKillSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "crash"},
				{Type: event.Click, Action: "increment"},
			},
		}

		handle := func(_ Session, s counterState, ev Event) counterState {
			if ev.Action == "crash" {
				panic("boom")
			}
			s.Count++
			return s
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = handle

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if s := sess.State(); s.Count != 1 {
			t.Errorf("expected Count 1 after recovery, got %d", s.Count)
		}

		mt.mu.Lock()
		defer mt.mu.Unlock()

		// Only the second event (increment) should produce a patch.
		if len(patchMessages(mt.sent)) != 1 {
			t.Errorf("expected 1 patch update after panic recovery, got %d", len(patchMessages(mt.sent)))
		}
	})
}

func TestSessionUpdatePanicDoesNotCrashCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		// Queue an update that panics. The panic is recovered inside
		// the command loop — the caller is not affected.
		sess.Update(func(s counterState) counterState {
			panic("boom in update")
		})
		synctest.Wait()

		// State should be unchanged after a panicking Update.
		if s := sess.State(); s.Count != 0 {
			t.Errorf("expected Count 0 after panicking Update, got %d", s.Count)
		}
	})
}

func TestServeInitialPagePanicDoesNotCrashProcess(t *testing.T) {
	handler := New(Config[counterState]{
		Mode:    mode.WebSocket,
		Upgrade: stubUpgrade,
		InitialState: func(r *http.Request) counterState {
			panic("boom in InitialState")
		},
		Render: renderCounter,
		Handle: handleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()

	// Should not panic — the recovery in serveInitialPage catches it.
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
