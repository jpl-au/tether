package poly

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sync"
	"time"

	jit "github.com/jpl-au/fluent-jit"
)

// Config configures a poly handler. The type parameter S is the session
// state — it can be any type (struct, int, string, etc.).
type Config[S any] struct {
	// Upgrade converts an HTTP request into a Transport connection.
	// Use ws.Upgrade for WebSocket connections.
	Upgrade func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// InitialState returns the starting state for a new session.
	// Called once per connection to create the initial state.
	InitialState func(r *http.Request) S

	// Render builds a node tree from the current state.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state.
	Handle HandleFunc[S]

	// OnConnect is called when a session is established. Optional.
	OnConnect func(session *Session[S])

	// OnDisconnect is called when a session ends. Optional.
	OnDisconnect func(session *Session[S])

	// Equal compares two states. If provided and returns true, the diff
	// is skipped entirely for that event. Optional.
	Equal func(a, b S) bool

	// Logger is used for session errors. Defaults to slog.Default().
	Logger *slog.Logger

	// MaxSessions limits the total number of concurrent sessions
	// (pending + active). Zero means unlimited.
	MaxSessions int

	// IdleTimeout closes sessions that receive no client events within
	// this duration. Zero means no idle timeout.
	IdleTimeout time.Duration

	// MaxLifetime closes sessions after this duration regardless of
	// activity. Zero means no maximum lifetime.
	MaxLifetime time.Duration
}

// New creates an http.Handler that manages poly sessions.
//
// GET requests receive the initial HTML page with the client JS injected.
// Requests with an Upgrade header start a session event loop.
func New[S any](cfg Config[S]) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	h := &handler[S]{
		cfg:     cfg,
		pending: make(map[string]*pendingSession[S]),
		active:  make(map[string]*Session[S]),
	}

	// Start a background reaper if any timeouts are configured.
	if cfg.IdleTimeout > 0 || cfg.MaxLifetime > 0 {
		go h.reap()
	}

	return h
}

// pendingSession holds a pre-warmed session created during the initial
// GET request, waiting for the WebSocket to attach.
type pendingSession[S any] struct {
	state     S
	differ    *jit.Differ
	createdAt time.Time
}

// pendingTimeout is the maximum time a pending session waits for a
// WebSocket connection before being discarded.
const pendingTimeout = 30 * time.Second

type handler[S any] struct {
	cfg     Config[S]
	mu      sync.Mutex
	pending map[string]*pendingSession[S]
	active  map[string]*Session[S]
}

func (h *handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") == "websocket" {
		h.serveSession(w, r)
		return
	}

	h.serveInitialPage(w, r)
}

// serveInitialPage renders the full HTML, pre-warms a session, and
// injects the client runtime. The session ID is embedded in the root
// element so the client can attach to it on WebSocket connect.
func (h *handler[S]) serveInitialPage(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.cfg.MaxSessions > 0 && len(h.pending)+len(h.active) >= h.cfg.MaxSessions {
		h.mu.Unlock()
		http.Error(w, "too many sessions", http.StatusServiceUnavailable)
		return
	}
	h.mu.Unlock()

	state := h.cfg.InitialState(r)
	tree := h.cfg.Render(state)

	differ := jit.NewDiffer()
	html := differ.Render(tree)

	id := newID()
	now := time.Now()
	h.mu.Lock()
	h.pending[id] = &pendingSession[S]{state: state, differ: differ, createdAt: now}
	h.mu.Unlock()

	var buf bytes.Buffer
	buf.WriteString(`<div data-poly-root data-poly-endpoint="`)
	buf.WriteString(r.URL.Path)
	buf.WriteString(`" data-poly-session="`)
	buf.WriteString(id)
	buf.WriteString(`">`)
	buf.Write(html)
	buf.WriteString("</div>\n<script src=\"/_poly/idiomorph.min.js\"></script>\n<script src=\"/_poly/fluent-poly.js\"></script>\n")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// serveSession upgrades the connection and runs the session event loop.
// If a session ID is provided via the query string, the pre-warmed session
// from the GET request is reused. Otherwise a fresh session is created.
func (h *handler[S]) serveSession(w http.ResponseWriter, r *http.Request) {
	transport, err := h.cfg.Upgrade(w, r)
	if err != nil {
		http.Error(w, "connection upgrade failed", http.StatusInternalServerError)
		return
	}

	id := r.URL.Query().Get("session")
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
		// No pre-warmed session found — create a fresh one.
		id = newID()
		state = h.cfg.InitialState(r)
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
		differ:       differ,
		transport:    transport,
		logger:       h.cfg.Logger,
		createdAt:    now,
		lastActivity: now,
	}

	if h.cfg.Equal != nil {
		sess.equal = h.cfg.Equal
	}

	// Register the session and wire up cleanup on disconnect.
	h.mu.Lock()
	h.active[id] = sess
	h.mu.Unlock()

	sess.onDisconnect = func() {
		h.mu.Lock()
		delete(h.active, id)
		h.mu.Unlock()
		if h.cfg.OnDisconnect != nil {
			h.cfg.OnDisconnect(sess)
		}
	}

	if h.cfg.OnConnect != nil {
		h.cfg.OnConnect(sess)
	}

	sess.run()
}

// reap periodically removes expired pending sessions and closes idle
// or long-lived active sessions. Runs in a background goroutine.
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

		// Close outside the lock to avoid holding it during I/O.
		for _, sess := range expired {
			h.cfg.Logger.Info("closing session", "session", sess.ID(), "idle", h.cfg.IdleTimeout > 0, "aged", h.cfg.MaxLifetime > 0)
			sess.Close()
		}
	}
}

// ServeClient returns an http.Handler that serves the embedded client JS files.
// Mount this at /_poly/ to serve fluent-poly.js and idiomorph.
func ServeClient() http.Handler {
	return http.FileServer(http.FS(clientFiles()))
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
