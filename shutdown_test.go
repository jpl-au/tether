package poly

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestShutdownClosesActiveSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()

		h := &Handler[counterState]{
			cfg:          Config[counterState]{},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       map[string]*Session[counterState]{"test": sess},
			disconnected: make(map[string]*Session[counterState]),
			done:         make(chan struct{}),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := h.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
		synctest.Wait()

		mt.mu.Lock()
		closed := mt.closed
		mt.mu.Unlock()

		if !closed {
			t.Error("expected transport to be closed after shutdown")
		}
	})
}

func TestShutdownStopsPendingCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &Handler[counterState]{
			cfg:          Config[counterState]{Timeouts: Timeouts{Pending: 30 * time.Second}},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       make(map[string]*Session[counterState]),
			disconnected: make(map[string]*Session[counterState]),
			done:         make(chan struct{}),
		}

		cleanupDone := false
		go func() {
			h.reapPending()
			cleanupDone = true
		}()

		close(h.done)
		synctest.Wait()

		if !cleanupDone {
			t.Fatal("pending cleanup goroutine did not exit after done channel closed")
		}
	})
}
