// Package poly is a reactive server-driven UI layer for Go. It connects
// Fluent node trees to the browser via a persistent transport (typically
// WebSocket), sending only the parts that changed as targeted patches.
// The client morphs the DOM in place using idiomorph, preserving input
// focus, scroll position, and form state.
//
// The central type is [Config], which wires together state, rendering,
// and event handling for a single page. Pass it to [New] to get an
// [http.Handler] that manages the full session lifecycle: initial HTML
// render, transport upgrade, event loop, diffing, reconnection, and
// idle/lifetime reaping.
//
// Updates flow through a unified protocol. Each message is a single
// "update" type containing either content patches (targeted key updates)
// or structural morphs (DOM changes applied via idiomorph). The
// [Transport] interface abstracts the wire — see the ws sub-package for
// the WebSocket implementation.
//
// Event binding helpers ([Click], [Submit], [Input], [Change], [KeyDown],
// [Focus], [Blur]) attach data-poly-* attributes to Fluent elements so
// the client JS knows which DOM events to forward.
package poly

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
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

	// ReconnectTimeout is how long a disconnected session is kept so the
	// client can reattach. Zero defaults to 30 seconds. Set to -1 to
	// disable reconnection (sessions are destroyed on disconnect).
	ReconnectTimeout time.Duration

	// Layout wraps the poly content in a full HTML document. The argument
	// is a node that renders the poly root div and client scripts. Return
	// a complete document tree (e.g. html.New(head.New(...), body.New(content))).
	//
	// When nil, the handler outputs a bare HTML fragment (the poly root
	// div and scripts only), which puts the browser in quirks mode.
	Layout func(content node.Node) node.Node
}

// New creates an http.Handler that manages poly sessions.
//
// GET requests receive the initial HTML page with the client JS injected.
// Requests with an Upgrade header start a session event loop.
// defaultReconnectTimeout is used when ReconnectTimeout is zero.
const defaultReconnectTimeout = 30 * time.Second

func New[S any](cfg Config[S]) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ReconnectTimeout == 0 {
		cfg.ReconnectTimeout = defaultReconnectTimeout
	}
	h := &handler[S]{
		cfg:          cfg,
		pending:      make(map[string]*pendingSession[S]),
		active:       make(map[string]*Session[S]),
		disconnected: make(map[string]*Session[S]),
	}

	// The reaper always runs to clean up pending and disconnected
	// sessions. It also enforces idle and lifetime limits when set.
	go h.reap()

	return h
}

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

// ServeHTTP routes requests: WebSocket upgrades start a session event
// loop, plain GET requests serve the initial HTML page.
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
	h.cfg.Logger.Info("serving initial page")

	h.mu.Lock()
	if h.cfg.MaxSessions > 0 && len(h.pending)+len(h.active)+len(h.disconnected) >= h.cfg.MaxSessions {
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

	content := &polyBody{html: html, endpoint: r.URL.Path, session: id}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if h.cfg.Layout != nil {
		h.cfg.Layout(content).Render(w)
	} else {
		content.Render(w)
	}
}

// serveSession upgrades the connection and runs the session event loop.
// It checks three pools in order:
//  1. Disconnected sessions (reconnecting client)
//  2. Pending sessions (initial page load)
//  3. Fresh session (fallback)
func (h *handler[S]) serveSession(w http.ResponseWriter, r *http.Request) {
	transport, err := h.cfg.Upgrade(w, r)
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

// ServeClient returns an http.Handler that serves the embedded client JS files.
// Mount this at /_poly/ to serve fluent-poly.js and idiomorph.
func ServeClient() http.Handler {
	return http.FileServer(http.FS(clientFiles()))
}

// polyBody is a node.Node that renders the poly root div (with the
// pre-rendered session content inside) and the client script tags.
// It exists so the Layout function receives a composable node rather
// than raw bytes.
type polyBody struct {
	html     []byte
	endpoint string
	session  string
}

func (p *polyBody) Render(w ...io.Writer) []byte {
	var buf bytes.Buffer
	p.RenderBuilder(&buf)
	if len(w) > 0 && w[0] != nil {
		buf.WriteTo(w[0])
		return nil
	}
	return buf.Bytes()
}

func (p *polyBody) RenderBuilder(buf *bytes.Buffer) {
	buf.WriteString(`<div data-poly-root data-poly-endpoint="`)
	buf.WriteString(p.endpoint)
	buf.WriteString(`" data-poly-session="`)
	buf.WriteString(p.session)
	buf.WriteString(`">`)
	buf.Write(p.html)
	buf.WriteString("</div>\n<script src=\"/_poly/idiomorph.min.js\"></script>\n<script src=\"/_poly/fluent-poly.js\"></script>\n")
}

func (p *polyBody) Nodes() []node.Node { return nil }

// newID generates a cryptographically random 32-character hex string
// for use as a session identifier.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
