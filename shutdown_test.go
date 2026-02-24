package poly

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestShutdownClosesActiveSessions(t *testing.T) {
	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

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

	mt.mu.Lock()
	closed := mt.closed
	mt.mu.Unlock()

	if !closed {
		t.Error("expected transport to be closed after shutdown")
	}
}

func TestShutdownStopsReaper(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &Handler[counterState]{
			cfg:          Config[counterState]{ReaperInterval: defaultReaperInterval},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       make(map[string]*Session[counterState]),
			disconnected: make(map[string]*Session[counterState]),
			done:         make(chan struct{}),
		}

		reaperDone := false
		go func() {
			h.reap()
			reaperDone = true
		}()

		close(h.done)
		synctest.Wait()

		if !reaperDone {
			t.Fatal("reaper did not exit after done channel closed")
		}
	})
}
