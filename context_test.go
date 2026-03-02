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

func TestSessionGoReceivesCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}

		sess := newTestSession(counterState{Count: 0}, mt)

		stopped := make(chan struct{})
		sess.Go(func(ctx context.Context) {
			<-ctx.Done()
			close(stopped)
		})

		// Cancel the session context (simulates reaper or shutdown).
		sess.stop()
		synctest.Wait()

		select {
		case <-stopped:
			// expected
		default:
			t.Fatal("goroutine should have stopped after context cancellation")
		}
	})
}

func TestSessionContextNilFallsBackToBackground(t *testing.T) {
	// A session without an initialised context (e.g. built manually
	// in tests) should return context.Background, not panic.
	sess := &Session[counterState]{}
	ctx := sess.Context()
	if ctx == nil {
		t.Fatal("Context() should never return nil")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("background context should not be cancelled, got %v", err)
	}
}
