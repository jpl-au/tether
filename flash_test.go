package tether

import (
	"testing"
	"testing/synctest"
)

func TestSessionFlashSendsUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.Flash("#notice", "Settings saved")
		synctest.Wait()

		ct.mu.Lock()
		defer ct.mu.Unlock()

		if len(ct.sent) != 1 {
			t.Fatalf("expected 1 update, got %d", len(ct.sent))
		}
		msg := decodeMessage(ct.sent[0])
		if msg.Flash == nil {
			t.Fatal("expected Flash map, got nil")
		}
		if msg.Flash["#notice"] != "Settings saved" {
			t.Errorf("Flash[#notice] = %q, want %q", msg.Flash["#notice"], "Settings saved")
		}
	})
}
