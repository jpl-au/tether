package poly

import (
	"testing"
	"testing/synctest"
)

func TestSessionSetTitleSendsUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.SetTitle("New Page")
		synctest.Wait()

		ct.mu.Lock()
		defer ct.mu.Unlock()

		if len(ct.sent) != 1 {
			t.Fatalf("expected 1 update, got %d", len(ct.sent))
		}
		msg := decodeMessage(ct.sent[0])
		if msg.Title != "New Page" {
			t.Errorf("expected title %q, got %q", "New Page", msg.Title)
		}
	})
}
