package poly

// HealthStatus reports the number of sessions in each pool. Use
// [Handler.Health] to retrieve it.
type HealthStatus struct {
	Pending      int `json:"pending"`
	Active       int `json:"active"`
	Disconnected int `json:"disconnected"`
}

// Health returns a snapshot of the session pool counts. Safe to call
// from any goroutine. Useful for load balancer health checks,
// readiness probes, or metrics collection.
func (h *Handler[S]) Health() HealthStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return HealthStatus{
		Pending:      len(h.pending),
		Active:       len(h.active),
		Disconnected: len(h.disconnected),
	}
}
