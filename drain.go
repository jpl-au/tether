package poly

import (
	"context"
	"sync"
	"time"
)

// Drain stops accepting new sessions but lets existing ones finish
// naturally. It blocks until all sessions have disconnected or ctx
// is cancelled. The background reaper continues running so idle and
// lifetime limits are still enforced during the drain period.
// Reconnecting clients can still reattach to their existing sessions.
//
// After Drain returns, call [Handler.Shutdown] to stop the reaper
// and release resources. Safe to call from any goroutine.
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

// Shutdown closes all active sessions and stops the background reaper.
// It blocks until every session has exited or ctx is cancelled. Safe
// to call more than once.
func (h *Handler[S]) Shutdown(ctx context.Context) error {
	h.closeOnce.Do(func() { close(h.done) })

	h.mu.Lock()
	sessions := make([]*Session[S], 0, len(h.active))
	for _, sess := range h.active {
		sessions = append(sessions, sess)
	}
	// Cancel disconnected session contexts before clearing — these
	// sessions will never be reclaimed.
	for _, sess := range h.disconnected {
		if sess.cancel != nil {
			sess.cancel()
		}
	}
	// The reaper is stopped (done is closed), so nothing else will
	// clean up these maps. Clear them to release memory.
	clear(h.pending)
	clear(h.disconnected)
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Go(func() {
			if sess.cancel != nil {
				sess.cancel()
			}
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
