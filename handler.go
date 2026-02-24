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
	"strings"
	"time"

	"github.com/jpl-au/fluent/node"
)

// TransportMode selects the wire protocol between server and browser.
// WebSocket gives bidirectional communication over a single connection.
// SSE+POST splits the channel: server→client updates flow over a
// long-lived EventSource stream, and client→server events arrive as
// individual HTTP POSTs. SSE+POST works through HTTP/2 reverse proxies
// and load balancers that may not support WebSocket, at the cost of
// slightly higher latency on client events.
type TransportMode int

const (
	// WebSocketOnly accepts only WebSocket connections. This is the
	// default when Mode is not set. The Fallback field is ignored.
	WebSocketOnly TransportMode = iota

	// SSEOnly accepts only SSE+POST connections. Use this when the
	// deployment environment does not support WebSocket (e.g. certain
	// PaaS providers or corporate proxies). The Upgrade field is
	// ignored; Fallback must be set.
	SSEOnly

	// WebSocketWithFallback tries WebSocket first. If the client
	// cannot establish a WebSocket connection (e.g. the proxy strips
	// the Upgrade header), it falls back to SSE+POST automatically.
	// Both Upgrade and Fallback must be set.
	WebSocketWithFallback
)

// Config wires together all the pieces of a poly page: how to create
// initial state, how to render it, and how to handle events. The type
// parameter S is the session state — typically a struct, but it can be
// any type. Each connected browser tab gets its own independent copy
// of S, so state is never shared across sessions unless you explicitly
// coordinate via [Group] or external storage.
//
// At minimum, set InitialState, Render, Handle, and either Upgrade or
// Fallback (depending on Mode). Everything else is optional and has
// sensible defaults.
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

	// OnConnect is called after a new session is created and its
	// transport is ready. Use this to add the session to a [Group]
	// for broadcasting, start background goroutines that push updates
	// via [Session.Update], or log the connection. Optional.
	OnConnect func(session *Session[S])

	// OnDisconnect is called after a session's transport closes (either
	// because the client disconnected or the session was reaped). Use
	// this to remove the session from a [Group] and clean up any
	// resources started in OnConnect. Optional.
	OnDisconnect func(session *Session[S])

	// Equal compares two states. When provided and the old and new state
	// are equal, the render and diff are skipped entirely — no work is
	// done and nothing is sent to the client. This is an optimisation
	// for handlers where many events leave state unchanged (e.g.
	// keystrokes that don't affect the model). Optional.
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

	// AllowedOrigins restricts WebSocket upgrades, SSE streams, and POST
	// events to requests whose Origin header matches one of these values.
	// This provides consistent CSRF protection across all transport types
	// from a single configuration point.
	//
	// Example: []string{"https://example.com", "https://staging.example.com"}
	//
	// When empty, the handler falls back to same-host checking (the
	// Origin header's host must match the request's Host header). This
	// is suitable for development but should be replaced with an
	// explicit list in production.
	AllowedOrigins []string

	// MaxEventBytes limits the size of a POST event body. Events carry
	// a type, action, and a map of string values (typically form fields).
	// Zero defaults to 64 KB. Increase this if your forms contain large
	// text fields (e.g. a rich-text editor).
	MaxEventBytes int64

	// PendingTimeout is how long a pre-warmed session waits for the
	// browser to open a transport connection. If the browser never
	// connects (e.g. the user closes the tab before the JS loads),
	// the session is discarded after this duration. Zero defaults to
	// 30 seconds.
	PendingTimeout time.Duration

	// ReaperInterval controls how often the background goroutine
	// checks for expired sessions (idle, lifetime, pending, and
	// disconnected). Shorter intervals detect expiry sooner at the
	// cost of slightly more CPU. Zero defaults to 15 seconds.
	ReaperInterval time.Duration

	// RetryDelay is the initial delay before the client JS attempts to
	// reconnect after a WebSocket close. The delay doubles on each
	// failed attempt up to MaxRetryDelay. Zero defaults to 1 second.
	RetryDelay time.Duration

	// MaxRetryDelay caps the exponential backoff for client reconnection
	// attempts. Zero defaults to 30 seconds.
	MaxRetryDelay time.Duration

	// DefaultDebounce is the debounce interval applied to input events
	// when the element does not specify data-poly-debounce. Zero
	// defaults to 300 milliseconds.
	DefaultDebounce time.Duration

	// TransitionTimeout is how long the client waits for a CSS
	// transitionend event before forcibly removing a leaving element.
	// This prevents nodes from getting stuck in the DOM when no CSS
	// transition is defined. Zero defaults to 5 seconds.
	TransitionTimeout time.Duration

	// HeartbeatInterval controls how often the SSE transport sends a
	// keep-alive comment to prevent intermediate proxies (AWS ALB,
	// Nginx, Cloudflare) from closing idle connections. Has no effect
	// on WebSocket transports which have their own ping/pong frames.
	// Zero defaults to 20 seconds. Set to -1 to disable heartbeats.
	HeartbeatInterval time.Duration

	// OnStructuralChange is called whenever the diff engine detects that
	// the render tree's structure has changed (Dynamic keys added,
	// removed, or reordered). Structural changes force a full root morph
	// instead of targeted patches, which is heavier for the client.
	//
	// Use this callback to track these occurrences in production via
	// telemetry or metrics. The change parameter describes exactly what
	// shifted so you can pinpoint which state transitions need keyed
	// containers. The callback runs under the session lock, so keep it
	// fast — offload any expensive work to a goroutine. Optional.
	OnStructuralChange func(session *Session[S], change StructuralChange)

	// Layout wraps the poly content in a full HTML document. The argument
	// is a node that renders the poly root div and client scripts. Return
	// a complete document tree (e.g. html.New(head.New(...), body.New(content))).
	//
	// When nil, the handler outputs a bare HTML fragment (the poly root
	// div and scripts only), which puts the browser in quirks mode.
	Layout func(content node.Node) node.Node
}

// defaultReconnectTimeout gives the client enough time to recover from
// brief network interruptions without keeping abandoned sessions alive.
const defaultReconnectTimeout = 30 * time.Second

// defaultMaxEventBytes is used when MaxEventBytes is zero.
const defaultMaxEventBytes = 64 << 10 // 64 KB

const defaultReaperInterval = 15 * time.Second

const defaultHeartbeatInterval = 20 * time.Second

// Defaults for the client-side JS runtime. These are passed to the
// browser as data attributes on the poly root element.
const (
	defaultRetryDelay        = 1000 * time.Millisecond
	defaultMaxRetryDelay     = 30 * time.Second
	defaultDefaultDebounce   = 300 * time.Millisecond
	defaultTransitionTimeout = 5 * time.Second
)

// New creates a [Handler] from the given configuration and starts a
// background reaper goroutine that enforces IdleTimeout, MaxLifetime,
// and ReconnectTimeout. The reaper runs for the lifetime of the handler;
// call [Handler.Shutdown] to stop it and close all active sessions
// before the process exits.
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
	if cfg.ReaperInterval == 0 {
		cfg.ReaperInterval = defaultReaperInterval
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

	// The reaper always runs to clean up pending and disconnected
	// sessions. It also enforces idle and lifetime limits when set.
	go h.reap()

	return h
}

// ServeHTTP implements http.Handler. A single endpoint serves all three
// request types: the initial HTML page (GET without upgrade headers),
// the transport connection (WebSocket upgrade or SSE stream), and POST
// events (SSE mode only). The Mode field in Config determines which
// transport paths are active. Requests that don't match any transport
// path fall through to the initial page render.
func (h *Handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		for _, allowed := range h.cfg.AllowedOrigins {
			if origin == allowed {
				return true
			}
		}
		return false
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

	// Read transport under the session lock because reattach writes
	// it concurrently when a disconnected session reconnects.
	sess.mu.Lock()
	t := sess.transport
	sess.mu.Unlock()

	pusher, ok := t.(EventPusher)
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

// ServeClient serves the embedded client runtime. Mount at /_poly/ so the
// HTML page can load fluent-poly.js and idiomorph.
func ServeClient() http.Handler {
	return http.FileServer(http.FS(clientFiles()))
}
