package tether

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestUpdateRefreshesLastActivity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		// Set lastActivity to the past so we can detect the change.
		past := time.Now().Add(-10 * time.Minute)
		sess.lastActivity.Store(past.UnixNano())

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.Update(func(s counterState) counterState {
			s.Count = 42
			return s
		})
		synctest.Wait()

		activity := sess.lastActivity.Load()
		if activity <= past.UnixNano() {
			t.Error("expected lastActivity to be refreshed")
		}
	})
}
