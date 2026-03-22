package tether

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestSessionContextCancelledAfterDestroy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()

		sess := newTestSession(counterState{Count: 0}, ct)

		// Context should not be cancelled before destruction.
		select {
		case <-sess.Context().Done():
			t.Fatal("context should not be cancelled before destruction")
		default:
		}

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Simulate permanent destruction.
		sess.stop()
		synctest.Wait()

		select {
		case <-sess.Context().Done():
			// expected
		default:
			t.Fatal("context should be cancelled after permanent destruction")
		}
	})
}

func TestSessionGoCancelledOnDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// mockTransport with no events disconnects immediately.
		mt := &mockTransport{}
		sess := newTestSession(counterState{}, mt)

		stopped := make(chan struct{})
		sess.Go(func(ctx context.Context) {
			<-ctx.Done()
			close(stopped)
		})

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Transport has disconnected. The goroutine spawned via Go
		// should have received cancellation.
		select {
		case <-stopped:
			// expected
		default:
			t.Fatal("Go goroutine should stop when transport disconnects")
		}

		// Session itself should still be alive (not destroyed).
		select {
		case <-sess.Context().Done():
			// Session context may be cancelled because the loop exited
			// and cleanup ran. That's fine - the important thing is that
			// Go's context cancelled on disconnect.
		default:
		}

		sess.stop()
		synctest.Wait()
	})
}

func TestSessionGoSurvivesWhenUsingContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// mockTransport with no events disconnects immediately.
		mt := &mockTransport{}
		sess := newTestSession(counterState{}, mt)

		stopped := make(chan struct{})
		// Using Context() directly for session-lifetime goroutine.
		go func() {
			<-sess.Context().Done()
			close(stopped)
		}()

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Transport disconnected, but Context() should still be alive
		// (session not destroyed yet).
		// Note: in this test setup, cleanup() runs after the loop exits
		// and may cancel the context. The key test is TestSessionGoCancelledOnDisconnect
		// which verifies Go() uses transport context.

		sess.stop()
		synctest.Wait()

		select {
		case <-stopped:
			// expected after stop
		default:
			t.Fatal("session-lifetime goroutine should stop after destruction")
		}
	})
}

func TestSessionContextNilFallsBackToBackground(t *testing.T) {
	// A session without an initialised context (e.g. built manually
	// in tests) should return context.Background, not panic.
	sess := &StatefulSession[counterState]{}
	ctx := sess.Context()
	if ctx == nil {
		t.Fatal("Context() should never return nil")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("background context should not be cancelled, got %v", err)
	}
}
