// Package poly is a reactive server-driven UI layer for Go. The server
// holds all application state and renders HTML; the browser is a thin
// display layer that forwards DOM events and morphs the page in place.
// This keeps business logic on the server while delivering the
// responsiveness of a single-page app.
//
// The lifecycle of a page visit is:
//
//  1. The browser GETs the page. The handler renders the initial HTML
//     from [Config].InitialState and [Config].Render, pre-warms a
//     session with the diff state, and embeds the session ID in the
//     root element.
//  2. The client JS opens a persistent transport (WebSocket or SSE)
//     and reclaims the pre-warmed session. Pre-warming avoids a second
//     render on connect and ensures the diff baseline matches the HTML
//     the browser already has.
//  3. When the user interacts with the page, the client sends an
//     [Event]. The server calls [Config].Handle to produce new state,
//     diffs the old and new render trees, and sends only the changed
//     fragments back as targeted patches or structural morphs.
//  4. The client applies patches by swapping innerHTML on keyed
//     elements, or applies morphs via idiomorph when the tree structure
//     changes. Idiomorph preserves input focus, scroll position, and
//     form state during morphs.
//
// The central type is [Config], which wires together state, rendering,
// and event handling. Pass it to [New] to get an [http.Handler] that
// manages the full session lifecycle.
//
// Event binding helpers ([Click], [Submit], [Input], [Change],
// [KeyDown], [Focus], [Blur]) attach data-poly-* attributes to Fluent
// elements so the client JS knows which DOM events to forward. See
// bind.go for the full set of helpers including client-side directives,
// loading states, and JS hooks.
package poly

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	closeOnce    sync.Once
	draining     atomic.Bool
}

// New creates a [Handler] from the given configuration. Session
// lifecycle is managed by per-session timers (idle, lifetime,
// disconnect) — there is no centralised reaper goroutine. A
// lightweight pending-cleanup goroutine removes pre-warmed sessions
// that are never claimed.
//
// Call [Handler.Shutdown] to cancel all sessions before the process
// exits.
func New[S any](cfg Config[S]) *Handler[S] {
	if cfg.InitialState == nil {
		panic("poly: Config.InitialState is required")
	}
	if cfg.Render == nil {
		panic("poly: Config.Render is required")
	}
	if cfg.Handle == nil {
		panic("poly: Config.Handle is required")
	}
	if cfg.Mode != SSEOnly && cfg.Upgrade == nil {
		panic("poly: Config.Upgrade is required for WebSocket mode")
	}
	if cfg.Mode != WebSocketOnly && cfg.Fallback == nil {
		panic("poly: Config.Fallback is required for SSE mode")
	}

	if cfg.Push != nil {
		cfg.Worker = true
	}
	if !cfg.DevMode && os.Getenv("POLY_DEV") != "" {
		cfg.DevMode = true
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ReconnectTimeout == 0 {
		cfg.ReconnectTimeout = defaultReconnectTimeout
	}
	if cfg.MaxEventBytes == 0 {
		cfg.MaxEventBytes = defaultMaxEventBytes
	}
	if cfg.PendingTimeout == 0 {
		cfg.PendingTimeout = defaultPendingTimeout
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = defaultRetryDelay
	}
	if cfg.MaxRetryDelay == 0 {
		cfg.MaxRetryDelay = defaultMaxRetryDelay
	}
	if cfg.DefaultDebounce == 0 {
		cfg.DefaultDebounce = defaultDefaultDebounce
	}
	if cfg.TransitionTimeout == 0 {
		cfg.TransitionTimeout = defaultTransitionTimeout
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
	}
	h := &Handler[S]{
		cfg:          cfg,
		pending:      make(map[string]*pendingSession[S]),
		active:       make(map[string]*Session[S]),
		disconnected: make(map[string]*Session[S]),
		done:         make(chan struct{}),
	}

	go h.reapPending()

	return h
}

// ServeHTTP implements http.Handler. A single endpoint serves all three
// request types: the initial HTML page (GET without upgrade headers),
// the transport connection (WebSocket upgrade or SSE stream), and POST
// events (SSE mode only). The Mode field in Config determines which
// transport paths are active. Requests that don't match any transport
// path fall through to the initial page render.
func (h *Handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Push subscription registrations arrive as POST with a special
	// header, regardless of transport mode. Handle them before the
	// mode switch to avoid being mistaken for an SSE event.
	if r.Method == "POST" && r.Header.Get("X-Poly-Push-Subscribe") == "true" {
		h.handlePushSubscribe(w, r)
		return
	}

	switch h.cfg.Mode {
	case SSEOnly:
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	case WebSocketWithFallback:
		if r.Header.Get("Upgrade") == "websocket" {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	default: // WebSocketOnly
		if r.Header.Get("Upgrade") == "websocket" {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
	}

	h.serveInitialPage(w, r)
}

// originAllowed checks the request's Origin header against
// Config.AllowedOrigins. When AllowedOrigins is configured, the Origin
// must match one of the listed values exactly. When AllowedOrigins is
// empty, it falls back to same-host checking as basic CSRF protection.
// Requests without an Origin header (e.g. same-origin navigations or
// non-browser clients) are always allowed.
func (h *Handler[S]) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(h.cfg.AllowedOrigins) > 0 {
		return slices.Contains(h.cfg.AllowedOrigins, origin)
	}
	// No AllowedOrigins configured — fall back to same-host check
	// as basic CSRF protection. Compare hostnames only so that
	// Origin: http://localhost matches Host: localhost:8080.
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return stripPort(u.Host) == stripPort(r.Host)
}

// stripPort returns the host portion of a host:port string. If there
// is no port, the input is returned unchanged.
func stripPort(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return host
}

// handlePostEvent receives a client event via HTTP POST. This is the
// client→server path for SSE mode, where the EventSource connection
// is unidirectional. WebSocket transports receive events on the
// socket itself and do not use this path.
func (h *Handler[S]) handlePostEvent(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	// The session ID is sent as a header rather than a query parameter
	// to keep it out of server access logs and browser history.
	id := r.Header.Get("X-Poly-Session")
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

	// The transport's PushEvent writes to a buffered channel that the
	// reader goroutine consumes — it does not touch session state.
	// The transport pointer is only modified by the loop (reattach),
	// and an active session always has a valid transport.
	pusher, ok := sess.transport.(EventPusher)
	if !ok {
		http.Error(w, "transport does not accept events", http.StatusMethodNotAllowed)
		return
	}

	// Cap the request body to prevent a malicious client from sending
	// a multi-gigabyte payload and exhausting server memory.
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxEventBytes)

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

// handlePushSubscribe receives a push subscription from the client JS
// after a successful PushManager.subscribe() call. The subscription is
// delivered to the PushConfig.OnSubscribe callback along with the
// session that sent it.
func (h *Handler[S]) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Push == nil || h.cfg.Push.OnSubscribe == nil {
		http.Error(w, "push not configured", http.StatusNotFound)
		return
	}
	if !h.originAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	id := r.Header.Get("X-Poly-Session")
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

	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxEventBytes)

	var sub PushSubscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}

	// Send the subscription to the session loop so it's stored
	// without racing with other loop operations.
	sess.cmds <- func() {
		sess.pushSub = &sub
	}

	go h.cfg.Push.OnSubscribe(sess, sub)
	w.WriteHeader(http.StatusNoContent)
}

// destroySession performs permanent cleanup for a session that is no
// longer reachable (reaped, shutdown, or disconnected with timeout -1).
// Cancelling the context causes the session loop to exit.
func (h *Handler[S]) destroySession(s *Session[S]) {
	if s.stop != nil {
		s.stop()
	}

	for _, g := range h.cfg.Groups {
		g.Remove(s)
	}
}
