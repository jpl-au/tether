package poly

import (
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

// pendingTimeout is the maximum time a pending session waits for a
// WebSocket connection before being discarded.
const pendingTimeout = 30 * time.Second

// handler is the core HTTP handler. It maintains three pools of sessions:
// pending (pre-warmed, waiting for WebSocket), active (connected and
// processing events), and disconnected (waiting for client to reconnect).
type handler[S any] struct {
	cfg          Config[S]
	mu           sync.Mutex
	pending      map[string]*pendingSession[S]
	active       map[string]*Session[S]
	disconnected map[string]*Session[S]
}

// serveInitialPage renders the full HTML, pre-warms a session, and
// injects the client runtime. The session ID is embedded in the root
// element so the client can attach to it on WebSocket connect.
func (h *handler[S]) serveInitialPage(w http.ResponseWriter, r *http.Request) {
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
	if h.cfg.Layout != nil {
		h.cfg.Layout(content).Render(w)
	} else {
		content.Render(w)
	}
}

// serveSession upgrades the connection and runs the session event loop.
// The upgrade function creates the transport — it may be the primary
// WebSocket upgrade or the SSE fallback. It checks three pools in order:
//  1. Disconnected sessions (reconnecting client)
//  2. Pending sessions (initial page load)
//  3. Fresh session (fallback)
func (h *handler[S]) serveSession(w http.ResponseWriter, r *http.Request, upgrade func(http.ResponseWriter, *http.Request) (Transport, error)) {
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

// reattach connects a new transport to a disconnected session and
// sends a full re-render so the client is in sync.
func (h *handler[S]) reattach(sess *Session[S], transport Transport) {
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

// wireDisconnect sets the onDisconnect callback for a session. On
// disconnect, the session is moved to the disconnected pool (if
// reconnection is enabled) or removed entirely.
func (h *handler[S]) wireDisconnect(sess *Session[S]) {
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

// reap periodically removes expired pending and disconnected sessions,
// and closes idle or long-lived active sessions.
func (h *handler[S]) reap() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		h.mu.Lock()

		// Remove pending sessions that were never claimed.
		for id, ps := range h.pending {
			if now.Sub(ps.createdAt) > pendingTimeout {
				delete(h.pending, id)
			}
		}

		// Remove disconnected sessions past the reconnect window.
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

		// Collect active sessions that should be closed.
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
