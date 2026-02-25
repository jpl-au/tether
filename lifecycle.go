package poly

import (
	"context"
	"net/http"
	"time"

	jit "github.com/jpl-au/fluent-jit"
)

// serveInitialPage handles the initial GET request. It pre-warms the
// session with the diff state and embeds the session ID in the root
// element so the client can reclaim it when the transport connects.
func (h *Handler[S]) serveInitialPage(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			h.cfg.Logger.Error("panic in initial render", "panic", v)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	h.cfg.Logger.Info("serving initial page")

	// Disconnected sessions are excluded from the limit because they
	// are not actively consuming transport resources — they are just
	// holding state in memory while waiting for the client to reconnect.
	// Including them would cause new connections to be rejected during
	// brief network interruptions when many clients disconnect at once.
	h.mu.Lock()
	if h.cfg.MaxSessions > 0 && len(h.pending)+len(h.active) >= h.cfg.MaxSessions {
		h.mu.Unlock()
		http.Error(w, "too many sessions", http.StatusServiceUnavailable)
		return
	}
	h.mu.Unlock()

	state := h.cfg.InitialState(r)
	if h.cfg.HandleParams != nil {
		state = h.cfg.HandleParams(state, Params{
			Path:  r.URL.Path,
			Query: r.URL.Query(),
		}).State
	}
	tree := h.cfg.Render(state)

	differ := jit.NewDiffer()
	html := differ.Render(tree)

	id := newID()
	now := time.Now()
	h.mu.Lock()
	h.pending[id] = &pendingSession[S]{state: state, differ: differ, createdAt: now}
	h.mu.Unlock()

	var pushKey string
	if h.cfg.Push != nil {
		pushKey = h.cfg.Push.VAPIDPublicKey
	}

	content := &polyBody{
		html:              html,
		endpoint:          r.URL.Path,
		session:           id,
		transport:         h.cfg.Mode,
		retryDelay:        h.cfg.RetryDelay,
		maxRetryDelay:     h.cfg.MaxRetryDelay,
		defaultDebounce:   h.cfg.DefaultDebounce,
		transitionTimeout: h.cfg.TransitionTimeout,
		worker:            h.cfg.Worker,
		pushKey:           pushKey,
		devMode:           h.cfg.DevMode,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Prevent session ID leakage via Referer header on external links.
	w.Header().Set("Referrer-Policy", "same-origin")
	if h.cfg.DevMode {
		w.Header().Set("Cache-Control", "no-store")
	}
	if h.cfg.Layout != nil {
		h.cfg.Layout(state, content).Render(w)
	} else {
		content.Render(w)
	}
}

// serveSession upgrades the connection and runs the session event loop.
// It checks pools in priority order — disconnected first (so a
// reconnecting client recovers its state), then pending (the normal
// path after a page load), and finally creates a fresh session as a
// fallback for direct transport connections without a prior GET.
func (h *Handler[S]) serveSession(w http.ResponseWriter, r *http.Request, upgrade func(http.ResponseWriter, *http.Request) (Transport, error)) {
	transport, err := upgrade(w, r)
	if err != nil {
		http.Error(w, "connection upgrade failed", http.StatusInternalServerError)
		return
	}

	// Close the transport if we exit before handing it to a session
	// event loop (e.g. a panic in InitialState or Render). Once
	// sess.run() starts, the event loop owns the transport lifecycle.
	running := false
	defer func() {
		if !running {
			transport.Close()
		}
	}()

	// Start keep-alive writes for transports that need them (SSE).
	// WebSocket has its own ping/pong and does not implement this.
	if hb, ok := transport.(Heartbeater); ok && h.cfg.HeartbeatInterval > 0 {
		hb.StartHeartbeat(h.cfg.HeartbeatInterval)
	}

	id := r.URL.Query().Get("session")

	// Try to reattach to a disconnected session.
	h.mu.Lock()
	if sess, ok := h.disconnected[id]; ok {
		delete(h.disconnected, id)
		h.active[id] = sess
		h.mu.Unlock()

		running = true
		h.reattach(sess, transport)
		return
	}
	h.mu.Unlock()

	// Try to claim a pending (pre-warmed) session.
	var state S
	var differ *jit.Differ

	h.mu.Lock()
	if ps, ok := h.pending[id]; ok {
		state = ps.state
		differ = ps.differ
		delete(h.pending, id)
	}
	h.mu.Unlock()

	if differ == nil {
		// This path handles direct transport connections without a prior
		// GET (e.g. bogus or missing session ID). Reject during drain
		// since this would create a brand new session.
		if h.draining.Load() {
			return
		}

		// Enforce MaxSessions here too, otherwise this path bypasses
		// the limit. Disconnected sessions are excluded for the same
		// reason as serveInitialPage.
		if h.cfg.MaxSessions > 0 {
			h.mu.Lock()
			full := len(h.pending)+len(h.active) >= h.cfg.MaxSessions
			h.mu.Unlock()
			if full {
				return
			}
		}

		id = newID()
		state = h.cfg.InitialState(r)
		if h.cfg.HandleParams != nil {
			state = h.cfg.HandleParams(state, Params{
				Path:  r.URL.Path,
				Query: r.URL.Query(),
			}).State
		}
		differ = jit.NewDiffer()

		tree := h.cfg.Render(state)
		differ.Render(tree)
	}

	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	sess := &Session[S]{
		id:           id,
		state:        state,
		render:       h.cfg.Render,
		handle:       h.cfg.Handle,
		handleParams: h.cfg.HandleParams,
		differ:       differ,
		transport:    transport,
		logger:       h.cfg.Logger,
		createdAt:    now,
		lastActivity: now,
		ctx:          ctx,
		cancel:       cancel,
	}

	if h.cfg.Equal != nil {
		sess.equal = h.cfg.Equal
	}
	if h.cfg.OnStructuralChange != nil {
		sess.onStructuralChange = h.cfg.OnStructuralChange
	}

	h.mu.Lock()
	h.active[id] = sess
	h.mu.Unlock()

	h.wireDisconnect(sess)

	if h.cfg.OnConnect != nil {
		h.cfg.OnConnect(sess)
	}

	for _, g := range h.cfg.Groups {
		g.Add(sess)
	}

	running = true
	sess.run()
}

// reattach reconnects a disconnected session with a new transport.
// A full re-render is sent because the client's DOM may have diverged
// while disconnected.
func (h *Handler[S]) reattach(sess *Session[S], transport Transport) {
	// The old transport's heartbeat goroutine stopped when it closed.
	// Start a fresh one for the new transport.
	if hb, ok := transport.(Heartbeater); ok && h.cfg.HeartbeatInterval > 0 {
		hb.StartHeartbeat(h.cfg.HeartbeatInterval)
	}

	sess.mu.Lock()
	sess.transport = transport
	sess.lastActivity = time.Now()
	sess.disconnectedAt = time.Time{}

	// Re-render current state and send to the client so it catches up.
	tree := sess.render(sess.state)
	html := sess.differ.Render(tree)
	sess.mu.Unlock()

	update := Update{
		Morphs: []Morph{{Key: "", HTML: html}},
	}
	if err := transport.SendUpdate(update); err != nil {
		sess.logger.Error("reattach send error", "session", sess.id, "err", err)
	}

	h.wireDisconnect(sess)
	sess.run()
}

// wireDisconnect installs the callback that moves a session into the
// disconnected pool (when reconnection is enabled) or removes it
// entirely. Must be called each time a transport is attached because
// the callback captures the handler's pool references.
func (h *Handler[S]) wireDisconnect(sess *Session[S]) {
	sess.onDisconnect = func() {
		// Write disconnectedAt under sess.mu before acquiring h.mu
		// to avoid nesting sess.mu inside h.mu. The reaper also
		// reads disconnectedAt under sess.mu (after releasing h.mu),
		// so this keeps the lock ordering consistent and prevents
		// a latent deadlock if any future code path acquires the
		// locks in the opposite order.
		if h.cfg.ReconnectTimeout > 0 {
			sess.mu.Lock()
			sess.disconnectedAt = time.Now()
			sess.mu.Unlock()
		}

		h.mu.Lock()
		delete(h.active, sess.id)
		if h.cfg.ReconnectTimeout > 0 {
			h.disconnected[sess.id] = sess
		} else {
			h.destroySession(sess)
		}
		h.mu.Unlock()

		if h.cfg.OnDisconnect != nil {
			h.cfg.OnDisconnect(sess)
		}

		// When reconnection is disabled, the session will never be
		// reclaimed — cancel its context so lifecycle-bound goroutines
		// started with Go() can clean up.
		if h.cfg.ReconnectTimeout <= 0 && sess.cancel != nil {
			sess.cancel()
		}
	}
}
