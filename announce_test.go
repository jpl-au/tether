package poly

import (
	"log/slog"
	"testing"
)

func TestSessionAnnounceSendsUpdate(t *testing.T) {
	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)
	sess.logger = slog.Default()

	sess.Announce("Item added")

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].Announce != "Item added" {
		t.Errorf("expected announce 'Item added', got %q", mt.updates[0].Announce)
	}
}

func TestSessionAnnounceDoesNotAffectState(t *testing.T) {
	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 5}, mt)
	sess.logger = slog.Default()

	sess.Announce("Hello")

	if sess.state.Count != 5 {
		t.Errorf("expected state unchanged (Count=5), got %d", sess.state.Count)
	}
}

func TestEncodeUpdateIncludesAnnounce(t *testing.T) {
	update := Update{Announce: "Screen reader text"}
	msg := EncodeUpdate(update)

	if msg.Announce != "Screen reader text" {
		t.Errorf("expected announce in encoded message, got %q", msg.Announce)
	}
}
