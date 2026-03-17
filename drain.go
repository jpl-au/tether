package tether

import (
	"context"
	"time"
)

// Compile-time check: *Handler must satisfy Drainable.
var _ Drainable = (*Handler[struct{}])(nil)

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
	sessions := make([]*LiveSession[S], 0, len(h.active)+len(h.disconnected))
	for _, sess := range h.active {
		sessions = append(sessions, sess)
	}
	for _, sess := range h.disconnected {
		sessions = append(sessions, sess)
	}
	// Clear pending and disconnected maps to release memory.
	clear(h.pending)
	clear(h.disconnected)
	h.mu.Unlock()

	// Cancel all sessions and close their transports.
	for _, sess := range sessions {
		sess.stop()
	}

	// Wait for every loop goroutine to exit (or the caller's
	// deadline, whichever comes first).
	done := make(chan struct{})
	go func() {
		for _, sess := range sessions {
			<-sess.destroyed
		}
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Loops have exited — state is stable, no goroutine is mutating
	// s.state. Save with context.Background() since session contexts
	// are cancelled. TTL uses the shutdown grace period as a recovery
	// window for the restarting server.
	if h.cfg.SessionStore != nil {
		ttl := h.cfg.Timeouts.ShutdownGrace
		if ttl == 0 {
			ttl = defaultShutdownGrace
		}
		for _, sess := range sessions {
			if sess.sessionStore != nil {
				sess.saveSessionState(context.Background(), ttl)
			}
		}
	}

	return nil
}
