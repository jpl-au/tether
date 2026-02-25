package poly

import (
	"log/slog"
	"net/http"
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
	// fast — offload any expensive work to a goroutine. Do not call
	// Session methods (Update, State, Navigate, ReplaceURL, SetTitle,
	// Close) from within this callback — the session mutex is already
	// held and these methods acquire it, causing a deadlock. Use
	// session.ID() for identification; it does not take the lock.
	// Optional.
	OnStructuralChange func(session *Session[S], change StructuralChange)

	// Layout wraps the poly content in a full HTML document. The argument
	// is a node that renders the poly root div and client scripts. Return
	// a complete document tree (e.g. html.New(head.New(...), body.New(content))).
	//
	// When nil, the handler outputs a bare HTML fragment (the poly root
	// div and scripts only), which puts the browser in quirks mode.
	Layout func(content node.Node) node.Node

	// Worker enables the service worker for asset caching, offline page
	// shells, and push notification support. When true, the client JS
	// registers /_poly/poly-worker.js as a service worker with scope
	// "/". Implicitly true when Push is configured. Default false.
	Worker bool

	// DevMode disables service worker registration and reloads the page
	// on disconnect instead of reconnecting. This ensures fresh assets
	// and state during development. Also sets Cache-Control: no-store
	// on the initial page response. Enable via this field or set the
	// POLY_DEV environment variable to any non-empty value.
	DevMode bool

	// Push enables Web Push notification support. When set, Worker is
	// implicitly true. Clients subscribe to push notifications after
	// connecting, and the subscription is delivered via OnSubscribe.
	// Use the push subpackage to send notifications. Optional.
	Push *PushConfig[S]
}

// PushConfig enables Web Push notifications for the page. When set on
// [Config], the service worker is implicitly enabled because push
// notifications require a service worker to receive messages when no
// tab is open.
type PushConfig[S any] struct {
	// VAPIDPublicKey is the base64url-encoded ECDSA P-256 public key
	// used to authenticate the application with the push service.
	// Generate a key pair with [push.GenerateVAPIDKeys].
	VAPIDPublicKey string

	// OnSubscribe is called when a client sends its push subscription
	// to the server. Store the subscription to send notifications later
	// via [push.Send]. The callback runs in its own goroutine so it is
	// safe to perform I/O (e.g. database writes). Optional.
	OnSubscribe func(session *Session[S], sub PushSubscription)
}

// PushSubscription holds the endpoint and encryption keys the browser
// provides after a successful pushManager.subscribe() call. Store this
// server-side to send notifications later via the push subpackage.
type PushSubscription struct {
	Endpoint string               `json:"endpoint"`
	Keys     PushSubscriptionKeys `json:"keys"`
}

// PushSubscriptionKeys holds the client-side ECDH public key and
// authentication secret needed to encrypt push message payloads.
type PushSubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
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
