package poly

import (
	"context"
	"testing"
	"time"
)

func TestSessionContextCancelledAfterRun(t *testing.T) {
	mt := &mockTransport{
		events: []Event{},
	}

	sess := newTestSession(counterState{Count: 0}, mt)

	// Context should not be cancelled before run.
	select {
	case <-sess.Context().Done():
		t.Fatal("context should not be cancelled before run")
	default:
	}

	sess.run()

	// Simulate permanent destruction (no reconnect).
	sess.cancel()

	select {
	case <-sess.Context().Done():
		// expected
	default:
		t.Fatal("context should be cancelled after permanent destruction")
	}
}

func TestSessionGoReceivesCancellation(t *testing.T) {
	mt := &mockTransport{
		events: []Event{},
	}

	sess := newTestSession(counterState{Count: 0}, mt)

	stopped := make(chan struct{})
	sess.Go(func(ctx context.Context) {
		<-ctx.Done()
		close(stopped)
	})

	// Cancel the session context (simulates reaper or shutdown).
	sess.cancel()

	select {
	case <-stopped:
		// expected
	case <-time.After(time.Second):
		t.Fatal("goroutine should have stopped after context cancellation")
	}
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
