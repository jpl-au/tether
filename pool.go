package poly

import (
	"context"
	"net/http"
	"sync"
	"time"

	jit "github.com/jpl-au/fluent-jit"
)

// pendingSession holds a pre-warmed session created during the initial GET
// request. The state and differ are seeded so that the WebSocket can attach
// without repeating the initial render.
type pendingSession[S any] struct {
	state     S
	differ    *jit.Differ
	createdAt time.Time
}

// defaultPendingTimeout is used when PendingTimeout is zero.
const defaultPendingTimeout = 30 * time.Second

// Handler manages the lifecycle of poly sessions. Sessions move through
// three pools — pending, active, and disconnected — so the server can
// pre-warm state on the initial GET and preserve it across brief network
// interruptions. Use Shutdown for graceful termination.
type Handler[S any] struct {
	cfg          Config[S]
	mu           sync.Mutex
	pending      map[string]*pendingSession[S]
	active       map[string]*Session[S]
	disconnected map[string]*Session[S]
	done         chan struct{}
}

// serveInitialPage handles the initial GET request. It pre-warms the
// session with the diff state and embeds the session ID in the root
// element so the client can reclaim it when the transport connects.
func (h *Handler[S]) serveInitialPage(w http.ResponseWriter, r *http.Request) {
	h.cfg.Logger.Info("serving initial page")

	h.mu.Lock()
	if h.cfg.MaxSessions > 0 && len(h.pending)+len(h.active)+len(h.disconnected) >= h.cfg.MaxSessions {
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
		})
	}
	tree := h.cfg.Render(state)

	differ := jit.NewDiffer()
	html := differ.Render(tree)

	id := newID()
	now := time.Now()
	h.mu.Lock()
	h.pending[id] = &pendingSession[S]{state: state, differ: differ, createdAt: now}
	h.mu.Unlock()

	content := &polyBody{html: html, endpoint: r.URL.Path, session: id, transport: h.cfg.Mode}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Prevent session ID leakage via Referer header on external links.
	w.Header().Set("Referrer-Policy", "same-origin")
	if h.cfg.Layout != nil {
		h.cfg.Layout(content).Render(w)
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

	id := r.URL.Query().Get("session")

	// Try to reattach to a disconnected session.
	h.mu.Lock()
	if sess, ok := h.disconnected[id]; ok {
		delete(h.disconnected, id)
		h.active[id] = sess
		h.mu.Unlock()

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
		id = newID()
		state = h.cfg.InitialState(r)
		if h.cfg.HandleParams != nil {
			state = h.cfg.HandleParams(state, Params{
				Path:  r.URL.Path,
				Query: r.URL.Query(),
			})
		}
		differ = jit.NewDiffer()

		tree := h.cfg.Render(state)
		differ.Render(tree)
	}

	now := time.Now()
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
	}

	if h.cfg.Equal != nil {
		sess.equal = h.cfg.Equal
	}

	h.mu.Lock()
	h.active[id] = sess
	h.mu.Unlock()

	h.wireDisconnect(sess)

	if h.cfg.OnConnect != nil {
		h.cfg.OnConnect(sess)
	}

	sess.run()
}

// reattach reconnects a disconnected session with a new transport.
// A full re-render is sent because the client's DOM may have diverged
// while disconnected.
func (h *Handler[S]) reattach(sess *Session[S], transport Transport) {
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
		h.mu.Lock()
		delete(h.active, sess.id)

		if h.cfg.ReconnectTimeout > 0 {
			sess.mu.Lock()
			sess.disconnectedAt = time.Now()
			sess.mu.Unlock()
			h.disconnected[sess.id] = sess
		}
		h.mu.Unlock()

		if h.cfg.OnDisconnect != nil {
			h.cfg.OnDisconnect(sess)
		}
	}
}

// Shutdown closes all active sessions and stops the background reaper.
// It blocks until every session has exited or ctx is cancelled.
func (h *Handler[S]) Shutdown(ctx context.Context) error {
	close(h.done)

	h.mu.Lock()
	sessions := make([]*Session[S], 0, len(h.active))
	for _, sess := range h.active {
		sessions = append(sessions, sess)
	}
	h.mu.Unlock()

	done := make(chan struct{})
	go func() {
		for _, sess := range sessions {
			sess.Close()
		}
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reap runs in a background goroutine, enforcing the lifecycle limits
// set in Config. It exits when the done channel is closed by Shutdown.
func (h *Handler[S]) reap() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
		}

		now := time.Now()
		h.mu.Lock()

		for id, ps := range h.pending {
			if now.Sub(ps.createdAt) > h.cfg.PendingTimeout {
				delete(h.pending, id)
			}
		}

		if h.cfg.ReconnectTimeout > 0 {
			for id, sess := range h.disconnected {
				sess.mu.Lock()
				expired := now.Sub(sess.disconnectedAt) > h.cfg.ReconnectTimeout
				sess.mu.Unlock()
				if expired {
					delete(h.disconnected, id)
				}
			}
		}

		var expired []*Session[S]
		for id, sess := range h.active {
			sess.mu.Lock()
			idle := h.cfg.IdleTimeout > 0 && now.Sub(sess.lastActivity) > h.cfg.IdleTimeout
			aged := h.cfg.MaxLifetime > 0 && now.Sub(sess.createdAt) > h.cfg.MaxLifetime
			sess.mu.Unlock()

			if idle || aged {
				expired = append(expired, sess)
				delete(h.active, id)
			}
		}

		h.mu.Unlock()

		for _, sess := range expired {
			h.cfg.Logger.Info("closing session", "session", sess.ID())
			sess.Close()
		}
	}
}
