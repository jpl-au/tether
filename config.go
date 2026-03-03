package tether

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jpl-au/fluent-tether/mode"
	"github.com/jpl-au/fluent-tether/push"
	"github.com/jpl-au/fluent-tether/wire"
	"github.com/jpl-au/fluent/node"
)

// Config wires together all the pieces of a tether page: how to create
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
	// is [mode.ServerSentEvents].
	Upgrade func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// Fallback converts an HTTP request into a Transport connection
	// using SSE+POST. Required when Mode is [mode.ServerSentEvents]
	// or [mode.Both]. Use sse.Upgrade() for SSE+POST.
	Fallback func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// Mode selects which transports the handler accepts. Defaults to
	// [mode.Both] when not set. See [mode] package for options.
	Mode mode.Transport

	// InitialState returns the starting state for a new session.
	// Called once per connection to create the initial state.
	InitialState func(r *http.Request) S

	// Render builds a node tree from the current state.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state. Side
	// effects (toast, navigate, title, etc.) are expressed as imperative
	// calls on the session parameter. In live mode the session is a
	// [*Session] which can be type-asserted for Update, Go, and Close.
	// See [HandleFunc] for concurrency constraints — Handle runs inside
	// the session's command loop and must not block.
	Handle HandleFunc[S]

	// Middleware wraps the Handle function with cross-cutting behaviour
	// such as logging, authentication, or metrics. Middleware fires for
	// all client events including navigation. Middleware is applied
	// outermost-first: the first entry in the slice is the outermost
	// layer of the chain. Optional.
	Middleware []Middleware[S]

	// OnNavigate processes a URL change and returns the new state.
	// Called on initial page load (after InitialState) and when the
	// browser navigates via link click or back/forward. If nil,
	// navigation events fall through to Handle.
	//
	// The session parameter is a [PreSession] because this function
	// runs both during pre-warming (initial GET, before a real session
	// exists) and during live navigation. Side-effect methods (SetTitle,
	// Toast, etc.) are always safe to call. During pre-warming, effects
	// are captured; during navigation, they are sent to the client.
	OnNavigate func(session PreSession, state S, params Params) S

	// OnConnect is called after a new session is created and its
	// transport is ready. Use this to set up subscriptions ([On],
	// [Observe]), join groups, start background goroutines that push
	// updates via [Session.Update], or log the connection. Optional.
	//
	// OnConnect runs on the HTTP handler goroutine after the session's
	// command loop has started but before the transport begins reading
	// client events. This means State, Update, On, Observe, and all
	// side-effect methods are safe to call. However, any blocking work
	// (slow database queries, HTTP calls) delays the session becoming
	// fully interactive — move heavy initialisation into [Session.Go].
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

	// OnStructuralChange is called whenever the diff engine detects that
	// the render tree's structure has changed (Dynamic keys added,
	// removed, or reordered). Structural changes force a full root morph
	// instead of targeted patches, which is heavier for the client.
	//
	// Use this callback to track these occurrences in production via
	// telemetry or metrics. The change parameter describes exactly what
	// shifted so you can pinpoint which state transitions need keyed
	// containers. When nil and DevMode is active, the framework logs a
	// debug message for each occurrence.
	//
	// The callback runs inside the session's command loop — keep it
	// fast and offload any expensive work to a goroutine. Optional.
	OnStructuralChange func(session *Session[S], change StructuralChange)

	// OnNoPatch is called when a render cycle produces no patches and
	// no structural change. This usually indicates a missing .Dynamic()
	// key, or an intentional signal-only update where the render tree
	// is unaffected.
	//
	// Use this to detect missing Dynamic keys during development, or
	// wire it into telemetry for production monitoring. When nil and
	// DevMode is active, the framework logs a debug message for each
	// occurrence.
	//
	// The callback runs inside the session's command loop — keep it
	// fast and offload any expensive work to a goroutine. Optional.
	OnNoPatch func(session *Session[S], info NoPatch)

	// Layout wraps the tether content in a full HTML document. The state
	// parameter is the session's initial state, which can be used to set
	// the page title or other document-level elements. The content
	// parameter is a node that renders the tether root div and client
	// scripts. Return a complete document tree (e.g.
	// html.New(head.New(...), body.New(content))).
	//
	// Layout runs once on the initial GET request. After that, only the
	// tether root div is morphed — the outer shell is not re-rendered.
	// To update shell elements during navigation or event handling, use
	// [Session.SetTitle] for the page title, and signal bindings
	// ([bind.BindText], [bind.BindClass], [bind.BindShow], etc.) for
	// everything else. Signal bindings work document-wide, so elements
	// in the Layout shell react to [Session.Signal] calls just like
	// elements inside the tether root.
	//
	// When nil, the handler outputs a bare HTML fragment (the tether root
	// div and scripts only), which puts the browser in quirks mode.
	Layout func(state S, content node.Node) node.Node

	// Logger is set as the slog default via slog.SetDefault. When nil,
	// the framework creates a text (or JSON, see LogJSON) handler at
	// INFO level (DEBUG in DevMode).
	Logger *slog.Logger

	// Worker enables the full service worker for asset caching, offline
	// page shells, and background sync. When true, the client JS
	// registers /_tether/fluent-tether-worker.js as a service worker with
	// scope "/". When false and Push is configured, a lightweight
	// push-only service worker is registered instead — it handles push
	// events without intercepting fetch requests or caching. Default
	// false.
	Worker bool

	// DevMode enables development conveniences: service workers are
	// unregistered (so assets are always fresh), the page reloads
	// automatically when the server comes back after a restart, debug
	// logging is enabled by default, and Cache-Control: no-store is
	// set on all responses. Enable via this field or set the TETHER_DEV
	// environment variable to any non-empty value.
	DevMode bool

	// LogJSON selects JSON output for the default logger instead of
	// text. Only applies when Logger is nil — if you provide your own
	// Logger, this field is ignored.
	LogJSON bool

	// Assets lists embedded asset collections to auto-serve. Each
	// [Asset] provides content-hashed
	// URLs for cache-busting. Assets are served at their configured
	// prefix (default "/assets/") with appropriate cache headers —
	// immutable in production, no-store in DevMode. Precache entries
	// are automatically injected into the service worker. Optional.
	Assets []*Asset

	// Upload enables file upload support. When set, the handler accepts
	// multipart POST requests from the upload extension JS and delivers
	// each file to the Handle callback. Optional.
	Upload *UploadConfig[S]

	// Push enables Web Push notification support. A lightweight
	// push-only service worker is registered automatically; set Worker
	// to true for the full service worker with caching and sync.
	// Subscription requires a user gesture via [bind.PushSubscribe].
	// Optional.
	Push *PushConfig[S]

	// Groups are collections that the session will automatically join
	// when its transport is ready and leave when the session is
	// permanently destroyed. Using Groups on Config avoids repetitive
	// Add/Remove boilerplate in OnConnect/OnDisconnect. Optional.
	Groups []*Group[S]

	// Timeouts groups all duration-based settings that control session
	// lifecycle, reconnection, and transport keep-alive timing.
	Timeouts Timeouts

	// Limits groups capacity constraints: session counts, channel
	// buffer sizes, and request body limits.
	Limits Limits

	// Client groups settings that are passed to the browser as data
	// attributes on the tether root element.
	Client Client

	// WireFormat selects the encoding for server-to-client updates.
	// Defaults to [wire.JSON]. Currently the only supported format;
	// additional formats (e.g. HTML fragments) will be added in future.
	WireFormat wire.Format

	// Security groups origin-checking and CSRF protection settings.
	Security Security
}

// Timeouts groups duration-based settings for session lifecycle,
// reconnection, and transport keep-alive.
type Timeouts struct {
	// Idle closes sessions that receive no client events within this
	// duration. Zero means no idle timeout.
	Idle time.Duration

	// MaxLifetime closes sessions after this duration regardless of
	// activity. Zero means no maximum lifetime.
	MaxLifetime time.Duration

	// Reconnect is how long a disconnected session is kept so the
	// client can reattach. Zero defaults to 30 seconds. Ignored when
	// DisableReconnect is true.
	Reconnect time.Duration

	// DisableReconnect destroys sessions immediately on disconnect
	// instead of keeping them for the Reconnect duration. Use this
	// when every connection should start fresh.
	DisableReconnect bool

	// Pending is how long a pre-warmed session waits for the browser
	// to open a transport connection. If the browser never connects
	// (e.g. the user closes the tab before the JS loads), the session
	// is discarded after this duration. Zero defaults to 30 seconds.
	Pending time.Duration

	// ShutdownGrace is how long [Handler.ListenAndServe] waits for
	// sessions to drain during graceful shutdown. After this period,
	// remaining sessions are force-closed. Zero defaults to 10 seconds.
	ShutdownGrace time.Duration

	// Heartbeat controls how often the SSE transport sends a keep-alive
	// comment to prevent intermediate proxies (AWS ALB, Nginx,
	// Cloudflare) from closing idle connections. Has no effect on
	// WebSocket transports which have their own ping/pong frames.
	// Zero defaults to 20 seconds. Ignored when DisableHeartbeat is
	// true.
	Heartbeat time.Duration

	// DisableHeartbeat stops the SSE transport from sending periodic
	// keep-alive comments. Only use this when you know that no
	// intermediate proxy will close idle connections.
	DisableHeartbeat bool

	// Retry is the initial delay before the client JS attempts to
	// reconnect after a WebSocket close. The delay doubles on each
	// failed attempt up to MaxRetry. Zero defaults to 1 second.
	Retry time.Duration

	// MaxRetry caps the exponential backoff for client reconnection
	// attempts. Zero defaults to 30 seconds.
	MaxRetry time.Duration
}

// Limits groups capacity constraints for sessions and requests.
type Limits struct {
	// MaxSessions limits the total number of concurrent sessions
	// (pending + active + disconnected). Zero means unlimited.
	MaxSessions int

	// CmdBufferSize sets the capacity of each session's internal
	// command channel. Commands include state updates, broadcasts,
	// and side effects. When the buffer is full, a short-lived
	// goroutine delivers the command to prevent cross-session
	// deadlocks during broadcasts. Zero defaults to 64.
	CmdBufferSize int

	// MaxEventBytes limits the size of a POST event body. Events carry
	// a type, action, and a map of string values (typically form
	// fields). Zero defaults to 64 KB. Increase this if your forms
	// contain large text fields (e.g. a rich-text editor).
	MaxEventBytes int64
}

// Client groups settings that control the browser-side JS runtime.
// These are passed to the browser as data attributes on the tether
// root element.
type Client struct {
	// DefaultDebounce is the debounce interval applied to input events
	// when the element does not specify data-tether-debounce. Zero
	// defaults to 300 milliseconds.
	DefaultDebounce time.Duration

	// TransitionTimeout is how long the client waits for a CSS
	// transitionend event before forcibly removing a leaving element.
	// This prevents nodes from getting stuck in the DOM when no CSS
	// transition is defined. Zero defaults to 5 seconds.
	TransitionTimeout time.Duration

	// FlashDuration is how long flash messages remain visible before
	// auto-clearing. Zero defaults to 5 seconds.
	FlashDuration time.Duration

	// ToastDuration is how long toast notifications remain visible
	// before animating out. Zero defaults to 5 seconds.
	ToastDuration time.Duration

	// BackgroundSync enables IndexedDB event queuing and background
	// sync for SSE mode. When true, failed POST events are stored in
	// IndexedDB and replayed on reconnect (or via the service worker's
	// Background Sync API). When false (default), failed events are
	// reported as errors and not retried.
	BackgroundSync bool
}

// Security groups origin-checking and CSRF protection settings.
type Security struct {
	// AllowedOrigins restricts WebSocket upgrades, SSE streams, and
	// POST events to requests whose Origin header matches one of these
	// values. This provides consistent CSRF protection across all
	// transport types from a single configuration point.
	//
	// Example: []string{"https://example.com", "https://staging.example.com"}
	//
	// When empty, the handler falls back to same-host checking (the
	// Origin header's host must match the request's Host header). This
	// is suitable for development but should be replaced with an
	// explicit list in production.
	AllowedOrigins []string
}

// PushConfig enables Web Push notifications for the page. The VAPID
// public key is passed to the client so it can subscribe when the user
// clicks a [bind.PushSubscribe] element. Subscription is never
// automatic — it always requires a user gesture.
type PushConfig[S any] struct {
	// Sender handles push notification delivery. Create with
	// [push.NewSender]. The sender's public key is automatically
	// used for client-side push subscription.
	Sender *push.Sender

	// OnSubscribe is called when a client sends its push subscription
	// to the server. Store the subscription to send notifications later
	// via [push.Sender.Send]. The callback runs in its own goroutine so
	// it is safe to perform I/O (e.g. database writes). Optional.
	OnSubscribe func(session *Session[S], sub push.Subscription)
}

// defaultReconnectTimeout gives the client enough time to recover from
// brief network interruptions without keeping abandoned sessions alive.
const defaultReconnectTimeout = 30 * time.Second

// defaultMaxEventBytes is used when MaxEventBytes is zero.
const defaultMaxEventBytes = 64 << 10 // 64 KB

const defaultHeartbeatInterval = 20 * time.Second

// Defaults for the client-side JS runtime. These are passed to the
// browser as data attributes on the tether root element.
const defaultShutdownGrace = 10 * time.Second

const (
	defaultRetryDelay        = 1000 * time.Millisecond
	defaultMaxRetryDelay     = 30 * time.Second
	defaultDefaultDebounce   = 300 * time.Millisecond
	defaultTransitionTimeout = 5 * time.Second
	defaultFlashDuration     = 5 * time.Second
	defaultToastDuration     = 5 * time.Second
)
