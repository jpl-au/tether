package tether

import "time"

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

	// MaxPending limits the number of pre-warmed sessions waiting
	// for a browser to open a transport connection. Each GET request
	// creates a pending session (state + differ), so this cap
	// protects against GET-flooding attacks where an attacker
	// scripts thousands of requests without ever connecting.
	// Pending sessions are cheap but unauthenticated — capping them
	// separately prevents an attacker from crowding out legitimate
	// active sessions under the global MaxSessions limit. Zero
	// defaults to 128.
	MaxPending int

	// CmdBufferSize sets the capacity of each session's internal
	// command channel. Commands include state updates, broadcasts,
	// and side effects. When the buffer is full, a short-lived
	// goroutine delivers the command to prevent cross-session
	// deadlocks during broadcasts. Each overflow emits a
	// [BufferOverflow] diagnostic. Sustained overflow usually
	// indicates a blocking [HandleFunc] or a broadcast rate that
	// exceeds the session's processing speed — increase the buffer
	// or move slow work into [LiveSession.Go]. Zero defaults to 64.
	CmdBufferSize int

	// MaxEventBytes limits the size of a POST event body. Events carry
	// a type, action, and a map of string values (typically form
	// fields). Zero defaults to 64 KB. Increase this if your forms
	// contain large text fields (e.g. a rich-text editor).
	MaxEventBytes int64

	// MaxPushSubscriptionBytes limits the size of a push subscription
	// POST body. Push subscriptions contain base64-encoded P-256 keys
	// and vendor-specific endpoint URLs that are typically larger than
	// UI events. Zero defaults to 4 KB.
	MaxPushSubscriptionBytes int64
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

	// SyncRetention controls how long queued events are retained in
	// IndexedDB before being discarded as stale. Events older than
	// this are deleted during replay rather than sent to the server.
	// Zero defaults to 1 hour. Only relevant when BackgroundSync is
	// enabled.
	SyncRetention time.Duration
}

// Security groups CSRF protection and session binding settings.
type Security struct {
	// TrustedOrigins lists origins that are allowed to make
	// state-changing requests. POST requests (events, uploads,
	// push subscriptions) are checked by Go 1.25's
	// [http.CrossOriginProtection]. WebSocket upgrades use a
	// dedicated check since they are GET requests that the stdlib
	// exempts as safe methods. Both use Sec-Fetch-Site as the
	// primary signal with Origin header comparison as a fallback.
	//
	// Safe read-only methods (initial page GET, SSE streams) are
	// always allowed regardless of origin.
	//
	// Example: []string{"https://example.com", "https://staging.example.com"}
	//
	// When empty, the handler falls back to same-host checking
	// (the Origin header's host:port must match the request's
	// Host header exactly). This is suitable for development but
	// should be replaced with an explicit list in production.
	TrustedOrigins []string

	// DisableSessionBinding turns off User-Agent verification on
	// session reconnect. By default, the framework captures the
	// User-Agent when a session is created and verifies it matches
	// on every reconnect. This detects stolen session IDs presented
	// from a different client. Disable only if your environment has
	// clients whose User-Agent changes mid-session.
	DisableSessionBinding bool
}

// defaultReconnectTimeout gives the client enough time to recover from
// brief network interruptions without keeping abandoned sessions alive.
const defaultReconnectTimeout = 30 * time.Second

// defaultMaxEventBytes is used when MaxEventBytes is zero.
const defaultMaxEventBytes = 64 << 10 // 64 KB

// defaultMaxPushSubscriptionBytes is used when MaxPushSubscriptionBytes
// is zero. Push subscriptions contain base64 keys (~130 bytes) and
// vendor-specific endpoint URLs (variable length). 4 KB is generous.
const defaultMaxPushSubscriptionBytes = 4 << 10 // 4 KB

const defaultHeartbeatInterval = 20 * time.Second

// defaultMaxPending caps the number of pre-warmed sessions waiting for
// a browser to connect. Pending sessions are cheap but unauthenticated,
// so the default protects against GET-flooding while being generous
// enough for legitimate traffic spikes.
const defaultMaxPending = 128

// defaultShutdownGrace is how long ListenAndServe waits for sessions to
// drain during graceful shutdown before force-closing them.
const defaultShutdownGrace = 10 * time.Second

const (
	defaultRetryDelay        = 1000 * time.Millisecond
	defaultMaxRetryDelay     = 30 * time.Second
	defaultDefaultDebounce   = 300 * time.Millisecond
	defaultTransitionTimeout = 5 * time.Second
	defaultFlashDuration     = 5 * time.Second
	defaultToastDuration     = 5 * time.Second
	defaultSyncRetention     = 1 * time.Hour
)
