package poly

import (
	"encoding/json"
	"testing"
	"testing/synctest"

	"github.com/jpl-au/fluent-poly/wire"
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

		if len(mt.sent) != 1 {
			t.Fatalf("expected 1 update, got %d", len(mt.sent))
		}
		msg := decodeMessage(mt.sent[0])
		if msg.Announce != "Item added" {
			t.Errorf("expected announce 'Item added', got %q", msg.Announce)
		}
	})
}

func TestEncodeUpdateIncludesAnnounce(t *testing.T) {
	u := wire.Update{Announce: "Screen reader text"}
	data, err := wire.JSONEncoder{}.Encode(u)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["announce"] != "Screen reader text" {
		t.Errorf("expected announce in encoded message, got %v", decoded["announce"])
	}
}
