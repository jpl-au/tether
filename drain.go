package tether

import "context"

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

	// Check immediately in case all pools are already empty.
	h.mu.RLock()
	empty := len(h.pending) == 0 && len(h.active) == 0 && len(h.disconnected) == 0
	h.mu.RUnlock()
	if empty {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.drainNotify:
		return nil
	}
}

// notifyDrain sends a non-blocking signal to drainNotify when all
// pools are empty and draining is active. Must be called while h.mu
// is held.
func (h *Handler[S]) notifyDrain() {
	if h.drainNotify == nil {
		return
	}
	if h.draining.Load() && len(h.pending) == 0 && len(h.active) == 0 && len(h.disconnected) == 0 {
		select {
		case h.drainNotify <- struct{}{}:
		default:
		}
	}
}

// Shutdown closes all active sessions and stops the pending cleanup
// goroutine. It blocks until every session's loop has exited or ctx
// is cancelled. Safe to call more than once.
//
// Shutdown waits for command loops, not for goroutines started via
// [Session.Go]. Those goroutines receive context cancellation and
// must exit promptly. A goroutine that ignores its context will leak
// and may race with the final state save. See [Session.Go].
func (h *Handler[S]) Shutdown(ctx context.Context) error {
	h.closeOnce.Do(func() { close(h.done) })

	h.mu.Lock()
	sessions := make([]*StatefulSession[S], 0, len(h.active)+len(h.disconnected))
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

	// Destroy all sessions. Frozen sessions keep their store
	// entries so a restarting server can restore them - only the
	// destroyed channel is closed to unblock waiters below.
	for _, sess := range sessions {
		if sess.freeze && Status(sess.status.Load()) == Frozen {
			sess.transition(Destroyed)
			sess.destroyedOnce.Do(func() { close(sess.destroyed) })
			continue
		}
		h.destroySession(sess)
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

	// Wait for active upload handlers to finish so temp files are
	// cleaned up before the process exits.
	uploadDone := make(chan struct{})
	go func() {
		h.uploadWG.Wait()
		close(uploadDone)
	}()
	select {
	case <-uploadDone:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Loops have exited - state is stable, no goroutine is mutating
	// s.state. Save with context.Background() since session contexts
	// are cancelled. TTL uses the shutdown grace period as a recovery
	// window for the restarting server.
	//
	// Skip frozen sessions: their state was already persisted during
	// onTransportClose and s.state has been zeroed. Saving here would
	// overwrite the valid snapshot with empty data.
	if h.cfg.SessionStore != nil {
		ttl := h.cfg.Timeouts.ShutdownGrace
		if ttl == 0 {
			ttl = defaultShutdownGrace
		}
		for _, sess := range sessions {
			if sess.sessionStore != nil && !sess.freeze {
				sess.saveSessionState(context.Background(), ttl)
			}
		}
	}

	return nil
}
