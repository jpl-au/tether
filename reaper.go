package poly

import (
	"sync"
	"time"
)

// reap runs in a background goroutine, enforcing the lifecycle limits
// set in Config. It exits when the done channel is closed by Shutdown.
//
// Each pool is scanned under its own lock acquisition so that a large
// active session map does not block new connections or reconnects while
// the reaper is checking idle/lifetime limits on other pools.
func (h *Handler[S]) reap() {
	ticker := time.NewTicker(h.cfg.ReaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
		}

		now := time.Now()

		h.reapPending(now)
		h.reapDisconnected(now)
		h.reapActive(now)
	}
}

// reapPending removes pre-warmed sessions whose browser never connected.
func (h *Handler[S]) reapPending(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for id, ps := range h.pending {
		if now.Sub(ps.createdAt) > h.cfg.PendingTimeout {
			delete(h.pending, id)
		}
	}
}

// reapDisconnected removes sessions whose reconnect window has elapsed.
// Uses the same snapshot-check-delete pattern as reapActive to avoid
// holding h.mu while acquiring session locks.
func (h *Handler[S]) reapDisconnected(now time.Time) {
	if h.cfg.ReconnectTimeout <= 0 {
		return
	}

	h.mu.Lock()
	checkList := make([]*Session[S], 0, len(h.disconnected))
	for _, sess := range h.disconnected {
		checkList = append(checkList, sess)
	}
	h.mu.Unlock()

	var expired []*Session[S]
	for _, sess := range checkList {
		sess.mu.Lock()
		past := now.Sub(sess.disconnectedAt) > h.cfg.ReconnectTimeout
		sess.mu.Unlock()
		if past {
			expired = append(expired, sess)
		}
	}

	if len(expired) > 0 {
		h.mu.Lock()
		for _, sess := range expired {
			if h.disconnected[sess.id] == sess {
				delete(h.disconnected, sess.id)
			}
		}
		h.mu.Unlock()

		for _, sess := range expired {
			h.destroySession(sess)
		}
	}
}

// reapActive closes sessions that have exceeded their idle or lifetime
// limits. The work is split into three phases so the handler lock is
// never held while acquiring individual session locks:
//
//  1. Snapshot session pointers under h.mu (fast — no session locks).
//  2. Check each session's timestamps under sess.mu (no handler lock).
//  3. Re-acquire h.mu to delete expired entries, re-verifying each one
//     in case a reconnect claimed the session between phases.
func (h *Handler[S]) reapActive(now time.Time) {
	// Phase 1: snapshot pointers so we can release h.mu quickly.
	h.mu.Lock()
	checkList := make([]*Session[S], 0, len(h.active))
	for _, sess := range h.active {
		checkList = append(checkList, sess)
	}
	h.mu.Unlock()

	// Phase 2: check timestamps without holding the handler lock.
	var expired []*Session[S]
	for _, sess := range checkList {
		sess.mu.Lock()
		idle := h.cfg.IdleTimeout > 0 && now.Sub(sess.lastActivity) > h.cfg.IdleTimeout
		aged := h.cfg.MaxLifetime > 0 && now.Sub(sess.createdAt) > h.cfg.MaxLifetime
		sess.mu.Unlock()

		if idle || aged {
			expired = append(expired, sess)
		}
	}

	// Phase 3: delete from the active map and close transports.
	if len(expired) > 0 {
		h.mu.Lock()
		for _, sess := range expired {
			// Re-verify the session is still the same pointer in the
			// active pool. A reconnect between phase 1 and now could
			// have replaced it.
			if h.active[sess.id] == sess {
				delete(h.active, sess.id)
			}
		}
		h.mu.Unlock()

		for _, sess := range expired {
			go func(s *Session[S]) {
				h.cfg.Logger.Info("closing session", "session", s.ID())
				s.Close()
				h.destroySession(s)
			}(sess)
		}
	}
}
