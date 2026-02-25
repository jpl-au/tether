package poly

import (
	"context"
	"sync"
	"time"
)

// Drain stops accepting new sessions but lets existing ones finish
// naturally. It blocks until all sessions have disconnected or ctx
// is cancelled. Per-session lifecycle timers continue enforcing idle
// and lifetime limits during the drain period. Reconnecting clients
// can still reattach to their existing sessions.
//
// After Drain returns, call [Handler.Shutdown] to stop the pending
// cleanup goroutine and release resources. Safe to call from any
// goroutine.
func (h *Handler[S]) Drain(ctx context.Context) error {
	h.draining.Store(true)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			h.mu.Lock()
			empty := len(h.pending) == 0 && len(h.active) == 0 && len(h.disconnected) == 0
			h.mu.Unlock()
			if empty {
				return nil
			}
		}
	}
}

// Shutdown closes all active sessions and stops the pending cleanup
// goroutine. It blocks until every session's loop has exited or ctx
// is cancelled. Safe to call more than once.
func (h *Handler[S]) Shutdown(ctx context.Context) error {
	h.closeOnce.Do(func() { close(h.done) })

	h.mu.Lock()
	sessions := make([]*Session[S], 0, len(h.active))
	for _, sess := range h.active {
		sessions = append(sessions, sess)
	}
	// Cancel disconnected session contexts — they will never be
	// reclaimed.
	for _, sess := range h.disconnected {
		sess.stop()
	}
	// Clear pending and disconnected maps to release memory.
	clear(h.pending)
	clear(h.disconnected)
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Go(func() {
			sess.stop()
			sess.Close()
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
