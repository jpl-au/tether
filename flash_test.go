package poly

import (
	"testing"
	"testing/synctest"
)

func TestSessionFlashSendsUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.Flash("#notice", "Settings saved")
		synctest.Wait()

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
	})
}
