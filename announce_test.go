package poly

import (
	"testing"
	"testing/synctest"
)

func TestSessionAnnounceSendsUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.Announce("Item added")
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		if len(mt.updates) != 1 {
			t.Fatalf("expected 1 update, got %d", len(mt.updates))
		}
		if mt.updates[0].Announce != "Item added" {
			t.Errorf("expected announce 'Item added', got %q", mt.updates[0].Announce)
		}
	})
}

func TestEncodeUpdateIncludesAnnounce(t *testing.T) {
	update := Update{Announce: "Screen reader text"}
	msg := EncodeUpdate(update)

	if msg.Announce != "Screen reader text" {
		t.Errorf("expected announce in encoded message, got %q", msg.Announce)
	}
}
