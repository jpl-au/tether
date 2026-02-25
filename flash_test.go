package poly

import (
	"log/slog"
	"testing"
)

func TestSessionFlashSendsUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.logger = slog.Default()

	sess.Flash("#notice", "Settings saved")

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	flash := mt.updates[0].Flash
	if flash == nil {
		t.Fatal("expected Flash map, got nil")
	}
	if flash["#notice"] != "Settings saved" {
		t.Errorf("Flash[#notice] = %q, want %q", flash["#notice"], "Settings saved")
	}
}

func TestSessionFlashDoesNotAffectState(t *testing.T) {
	mt := &mockTransport{
		events: []Event{},
	}

	sess := newTestSession(counterState{Count: 5}, mt)
	sess.logger = slog.Default()

	sess.Flash(".alert", "Done")

	if sess.state.Count != 5 {
		t.Errorf("state.Count = %d, want 5", sess.state.Count)
	}
}
