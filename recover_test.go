package poly

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionHandlePanicDoesNotKillSession(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "crash"},
			{Type: "click", Action: "increment"},
		},
	}

	handle := func(_ *Session[counterState], s counterState, ev Event) HandleResult[counterState] {
		if ev.Action == "crash" {
			panic("boom")
		}
		s.Count++
		return Result(s)
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.handle = handle
	sess.logger = slog.Default()

	// Should not panic — the session recovers and processes the second event.
	sess.run()

	if sess.state.Count != 1 {
		t.Errorf("expected Count 1 after recovery, got %d", sess.state.Count)
	}

	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Only the second event (increment) should produce a patch.
	if len(patchUpdates(mt.updates)) != 1 {
		t.Errorf("expected 1 patch update after panic recovery, got %d", len(patchUpdates(mt.updates)))
	}
}

func TestSessionUpdatePanicDoesNotCrashCaller(t *testing.T) {
	mt := &mockTransport{
		events: []Event{},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.logger = slog.Default()

	// Should not panic — the recovery in Update catches it.
	sess.Update(func(s counterState) HandleResult[counterState] {
		panic("boom in update")
	})

	// State should be unchanged after a panicking Update.
	if sess.state.Count != 0 {
		t.Errorf("expected Count 0 after panicking Update, got %d", sess.state.Count)
	}
}

func TestServeInitialPagePanicDoesNotCrashProcess(t *testing.T) {
	handler := New(Config[counterState]{
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
