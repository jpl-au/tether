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
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jpl-au/fluent/node"
)

// TransportMode controls which transports the handler accepts.
type TransportMode int

const (
	// WebSocketOnly accepts only WebSocket connections. This is the
	// default when Mode is not set. The Fallback field is ignored.
	WebSocketOnly TransportMode = iota

	// SSEOnly accepts only SSE+POST connections. The Upgrade field is
	// ignored; Fallback must be set.
	SSEOnly

	// WebSocketWithFallback tries WebSocket first. If the client cannot
	// establish a WebSocket connection, it falls back to SSE+POST
	// automatically. Both Upgrade and Fallback must be set.
	WebSocketWithFallback
)

// Config configures a poly handler. The type parameter S is the session
// state — it can be any type (struct, int, string, etc.).
type Config[S any] struct {
	// Upgrade converts an HTTP request into a Transport connection.
	// Use ws.Upgrade for WebSocket connections. Required unless Mode
	// is SSEOnly.
	Upgrade func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// Fallback converts an HTTP request into a Transport connection
	// using SSE+POST. Required when Mode is SSEOnly or
	// WebSocketWithFallback. Use sse.Upgrade() for SSE+POST.
	Fallback func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// Mode selects which transports the handler accepts. Defaults to
	// WebSocketOnly. See TransportMode constants.
	Mode TransportMode

	// InitialState returns the starting state for a new session.
	// Called once per connection to create the initial state.
	InitialState func(r *http.Request) S

	// Render builds a node tree from the current state.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state.
	Handle HandleFunc[S]

	// HandleParams processes a URL change and returns updated state.
	// Called on initial page load (after InitialState) and when the
	// browser navigates via link click or back/forward. If nil,
	// navigation events fall through to Handle.
	HandleParams func(state S, params Params) S

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

// defaultReconnectTimeout is used when ReconnectTimeout is zero.
const defaultReconnectTimeout = 30 * time.Second

// New creates an http.Handler that manages poly sessions.
//
// GET requests receive the initial HTML page with the client JS injected.
// Requests with an Upgrade header start a session event loop.
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

// ServeHTTP routes requests by type. The routing depends on the configured
// TransportMode: WebSocketOnly accepts only WebSocket upgrades, SSEOnly
// accepts only SSE streams and POST events, and WebSocketWithFallback
// tries WebSocket first with SSE as a backup.
func (h *handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch h.cfg.Mode {
	case SSEOnly:
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	case WebSocketWithFallback:
		if r.Header.Get("Upgrade") == "websocket" {
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	default: // WebSocketOnly
		if r.Header.Get("Upgrade") == "websocket" {
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
	}

	h.serveInitialPage(w, r)
}

// handlePostEvent routes an incoming POST event to an active session's
// transport. The transport must implement EventPusher (e.g. the SSE
// transport). WebSocket transports do not — they receive events on
// the WebSocket connection directly.
func (h *handler[S]) handlePostEvent(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session")
	if id == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	sess, ok := h.active[id]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	pusher, ok := sess.transport.(EventPusher)
	if !ok {
		http.Error(w, "transport does not accept events", http.StatusMethodNotAllowed)
		return
	}

	var ev Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}

	if err := pusher.PushEvent(ev); err != nil {
		if errors.Is(err, ErrEventBufferFull) {
			http.Error(w, "event buffer full", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "session closed", http.StatusGone)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ServeClient returns an http.Handler that serves the embedded client JS files.
// Mount this at /_poly/ to serve fluent-poly.js and idiomorph.
func ServeClient() http.Handler {
	return http.FileServer(http.FS(clientFiles()))
}
