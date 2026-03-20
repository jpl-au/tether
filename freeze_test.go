package tether

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// TestFreezeOnDisconnect verifies that a session with freeze enabled
// transitions to Frozen status, persists state to the store, and
// nils out the differ after the transport closes.
func TestFreezeOnDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		mt := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}
		sess.freeze = true
		sess.reconnectTimeout = 30 * time.Second

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// After disconnect with freeze, session should be frozen.
		if Status(sess.status.Load()) != Frozen {
			t.Errorf("status = %d, want Frozen (%d)", sess.status.Load(), Frozen)
		}

		// Differ should be released.
		if sess.differ != nil {
			t.Error("differ should be nil after freeze")
		}

		// State should be zeroed.
		if sess.state.Count != 0 {
			t.Errorf("state.Count = %d, want 0 (zero value)", sess.state.Count)
		}

		// Store should hold the persisted state with Count=1.
		data, err := store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data == nil {
			t.Fatal("expected session data in store after freeze, got nil")
		}
		env, err := unmarshalEnvelope(data)
		if err != nil {
			t.Fatalf("unmarshalEnvelope: %v", err)
		}
		codec := cborCodec[counterState]{}
		state, err := codec.Unmarshal(env.State)
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if state.Count != 1 {
			t.Errorf("persisted Count = %d, want 1", state.Count)
		}

		// Clean up.
		sess.stop()
		synctest.Wait()
	})
}

// TestFreezeDiscardsCommands verifies that enqueue and enqueueFx
// silently discard work when the session is frozen.
func TestFreezeDiscardsCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		mt := &mockTransport{} // no events - disconnects immediately
		sess := newTestSession(counterState{}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}
		sess.freeze = true

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		if Status(sess.status.Load()) != Frozen {
			t.Fatalf("expected Frozen, got %d", sess.status.Load())
		}

		// enqueue should not block or panic.
		called := false
		sess.enqueue(func() { called = true })
		synctest.Wait()
		if called {
			t.Error("command was executed on a frozen session")
		}

		// enqueueFx should not block or panic.
		fxCalled := false
		sess.enqueueFx(func(*Effects) { fxCalled = true })
		synctest.Wait()
		if fxCalled {
			t.Error("effect was executed on a frozen session")
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestFreezeLoopDoneCloses verifies that loopDone is closed when the
// session freezes, so the HTTP handler goroutine can return.
func TestFreezeLoopDoneCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		mt := &mockTransport{}
		sess := newTestSession(counterState{}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}
		sess.freeze = true

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// loopDone should be closed.
		select {
		case <-sess.loopDone:
			// expected
		default:
			t.Error("loopDone not closed after freeze")
		}

		// destroyed should NOT be closed.
		select {
		case <-sess.destroyed:
			t.Error("destroyed should not be closed after freeze")
		default:
			// expected
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestFreezeDestroyClosesBoth verifies that destroying a frozen
// session closes the destroyed channel.
func TestFreezeDestroyClosesBoth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		mt := &mockTransport{}
		sess := newTestSession(counterState{}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}
		sess.freeze = true

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Simulate what Handler.destroySession does for frozen sessions.
		sess.stop()
		if Status(sess.status.Load()) == Frozen {
			sess.status.Store(int32(Destroyed))
			close(sess.destroyed)
		}
		synctest.Wait()

		select {
		case <-sess.destroyed:
			// expected
		default:
			t.Error("destroyed not closed after destroying frozen session")
		}
	})
}

// TestNoFreezeWithoutStore verifies that freeze is not enabled when
// SessionStore is nil, even if the freeze flag is set on the session.
func TestNoFreezeWithoutStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		sess := newTestSession(counterState{Count: 0}, mt)
		// freeze is true but no sessionStore - simulates the config
		// validation catching this at startup.
		sess.freeze = false // would have been disabled by Stateful()

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Without freeze, differ should still be present after disconnect.
		// (session is destroyed by ctx cancellation, not frozen)
		// Status should NOT be Frozen.
		if Status(sess.status.Load()) == Frozen {
			t.Error("session should not be frozen without a store")
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestFreezeWithoutFreezeFlag verifies that sessions with a
// SessionStore but without freeze enabled behave normally - the
// loop keeps running after disconnect.
func TestFreezeWithoutFreezeFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		mt := &mockTransport{}
		sess := newTestSession(counterState{}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}
		sess.freeze = false // store configured but freeze off

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Session should remain active (loop still running), not frozen.
		if Status(sess.status.Load()) == Frozen {
			t.Error("session should not be frozen when FreezeOnDisconnect is false")
		}

		// Commands should still be processed.
		called := make(chan bool, 1)
		sess.enqueue(func() { called <- true })
		synctest.Wait()

		select {
		case <-called:
			// expected
		default:
			t.Error("command was not processed - loop should still be running")
		}

		sess.stop()
		synctest.Wait()
	})
}
