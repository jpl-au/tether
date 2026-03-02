package tether

import (
	"testing"
	"testing/synctest"
)

// TestStateBeforeLoopDoesNotDeadlock verifies that calling State()
// before the run loop has started returns the state directly instead
// of deadlocking. This is the scenario that caused the original bug:
// OnConnect called State() while serveSession had not yet started
// the goroutines.
func TestStateBeforeLoopDoesNotDeadlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 42}, ct)

		// State() before run() — must not deadlock.
		got := sess.State()
		if got.Count != 42 {
			t.Errorf("State() before loop: got Count=%d, want 42", got.Count)
		}

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()
	})
}

// TestOnConnectCanCallState mimics the fixed serveSession flow:
// run() starts first, then OnConnect fires and calls State().
// This must complete without deadlocking.
func TestOnConnectCanCallState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 7}, ct)

		// Start the loop first (the fix).
		go sess.run()
		synctest.Wait()

		// Simulate OnConnect calling State().
		got := sess.State()
		if got.Count != 7 {
			t.Errorf("State() during OnConnect: got Count=%d, want 7", got.Count)
		}

		go sess.readTransport(sess.events)
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()
	})
}
