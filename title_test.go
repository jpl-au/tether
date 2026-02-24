package poly

import (
	"log/slog"
	"testing"
)

func TestSessionSetTitleSendsUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.logger = slog.Default()

	sess.SetTitle("New Page")

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].Title != "New Page" {
		t.Errorf("expected title %q, got %q", "New Page", mt.updates[0].Title)
	}
}
