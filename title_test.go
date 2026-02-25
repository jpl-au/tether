package poly

import (
	"testing"
	"testing/synctest"
)

func TestSessionSetTitleSendsUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.SetTitle("New Page")
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		if len(mt.updates) != 1 {
			t.Fatalf("expected 1 update, got %d", len(mt.updates))
		}
		if mt.updates[0].Title != "New Page" {
			t.Errorf("expected title %q, got %q", "New Page", mt.updates[0].Title)
		}
	})
}
