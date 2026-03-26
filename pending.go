package tether

import "time"

// reapPending runs in a background goroutine, periodically removing
// pre-warmed sessions whose browser never connected. It exits when
// the done channel is closed by Shutdown.
func (h *Handler[S]) reapPending() {
	ticker := time.NewTicker(h.cfg.Timeouts.PendingCheck)
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
			h.notifyDrain()
			h.mu.Unlock()
		}
	}
}
