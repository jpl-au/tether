package tether

import "time"

// pendingCheckInterval controls how often expired pending sessions are
// cleaned up. This is the only polling that remains after removing the
// centralised reaper - active and disconnected sessions use per-session
// timers instead.
const pendingCheckInterval = 10 * time.Second

// reapPending runs in a background goroutine, periodically removing
// pre-warmed sessions whose browser never connected. It exits when
// the done channel is closed by Shutdown.
func (h *Handler[S]) reapPending() {
	ticker := time.NewTicker(pendingCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.done:
			return
		case now := <-ticker.C:
			h.mu.Lock()
			for id, ps := range h.pending {
				if now.Sub(ps.createdAt) > h.cfg.Timeouts.Pending {
					delete(h.pending, id)
				}
			}
			h.mu.Unlock()
		}
	}
}
