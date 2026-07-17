package tether

import (
	"time"

	"github.com/jpl-au/tether/dev"
)

// Timeouts groups duration-based settings for session lifecycle,
// reconnection, and transport keep-alive.
type Timeouts struct {
	// Idle closes sessions with no activity within this duration.
	// Activity includes client events, Update and Patch calls, and
	// server-push effects (Signal, Toast, etc.), so a session the
	// server is actively updating never counts as idle. Zero means
	// no idle timeout.
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

	// Heartbeat controls how often transports send keep-alive frames.
	// SSE sends comment lines to prevent proxy timeouts. WebSocket
	// sends ping frames and sets read deadlines to detect silently
	// dropped connections. Zero defaults to 20 seconds. Ignored when
	// DisableHeartbeat is true.
	Heartbeat time.Duration

	// DisableHeartbeat stops transports from sending periodic
	// keep-alive frames. For SSE this risks proxy timeouts. For
	// WebSocket this disables dead-connection detection - sessions
	// with no idle timeout may hang indefinitely if a middlebox
	// silently drops the connection.
	DisableHeartbeat bool

	// Retry is the initial delay before the client JS attempts to
	// reconnect after a transport close. The delay grows by
	// BackoffMultiplier on each failed attempt up to MaxRetry.
	// Zero defaults to 500 milliseconds.
	Retry time.Duration

	// MaxRetry caps the exponential backoff for client reconnection
	// attempts. Zero defaults to 10 seconds.
	MaxRetry time.Duration

	// BackoffMultiplier controls how aggressively the retry delay
	// grows after each failed reconnection attempt. The delay after
	// attempt N is: Retry * BackoffMultiplier^N, capped at MaxRetry.
	// Zero defaults to 1.5. Values below 1 are treated as the
	// default.
	BackoffMultiplier float64

	// DisableJitter turns off the randomisation applied to each retry
	// delay. Without jitter, all clients that disconnect at the same
	// time will reconnect in lockstep (thundering herd). With jitter
	// (the default), each delay is multiplied by a random factor in
	// [0.5, 1.0), spreading clients across time.
	DisableJitter bool

	// PendingCheck controls how often the background goroutine scans
	// for expired pending sessions (pre-warmed sessions whose browser
	// never connected). This is the only polling in the framework -
	// active and disconnected sessions use per-session timers. Zero
	// defaults to 10 seconds.
	PendingCheck time.Duration

	// SlowRender emits a [SlowRender] diagnostic when a render+diff
	// cycle exceeds this duration. Use this to detect expensive render
	// functions in production and identify candidates for memoisation.
	// Zero disables the diagnostic.
	SlowRender time.Duration

	// MemoiseMissThreshold emits a [HighMemoiseMissRate] diagnostic when
	// the proportion of memoisation cache misses in a render cycle exceeds
	// this value. For example, 0.8 fires when more than 80% of memoised
	// nodes miss. Only applies when [StatefulConfig].Memoise is true.
	// Zero disables the diagnostic.
	MemoiseMissThreshold float64
}

// Limits groups capacity constraints for sessions and requests.
type Limits struct {
	// CmdBufferSize sets the capacity of each session's internal
	// command channel. Commands include state updates, broadcasts,
	// and side effects. When the buffer is full, a short-lived
	// goroutine delivers the command to prevent cross-session
	// deadlocks during broadcasts. Each overflow emits a
	// [BufferOverflow] diagnostic. Sustained overflow usually
	// indicates a blocking [HandleFunc] or a broadcast rate that
	// exceeds the session's processing speed - increase the buffer
	// or move slow work into [StatefulSession.Go]. Zero defaults to 64.
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

	// MaxStateBytes warns when the serialised session state exceeds
	// this size. The save still proceeds - this is a diagnostic
	// guardrail, not a hard limit. Large state increases write
	// amplification on disconnect and reconnect. Zero disables the
	// check.
	MaxStateBytes int64

	// MaxNavigateRedirects caps how many consecutive server-side
	// redirects the framework resolves within a single navigate event.
	// When OnNavigate calls Navigate(), the framework re-processes the
	// target URL inline rather than round-tripping to the client. This
	// limit prevents infinite loops when OnNavigate unconditionally
	// redirects. Zero defaults to 5.
	MaxNavigateRedirects int
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

	// ViewTransitions wraps each morph/patch application in a native
	// View Transition (document.startViewTransition) so DOM updates
	// cross-fade instead of snapping. It is a single opt-in switch: when
	// true and the browser supports the API, every server-driven update
	// animates; when false, unsupported, or the user has
	// prefers-reduced-motion set, updates apply instantly exactly as
	// before. Scope individual elements with the native CSS
	// view-transition-name property - tether adds no per-element API.
	ViewTransitions bool

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

	// Runtime selects the browser-side client implementation injected
	// after the tether root element. When nil, the framework uses the
	// default JS runtime (idiomorph and tether.js). Set to
	// Runtime.WASM() to use a Go WASM client instead.
	//
	//     tether.Client{
	//         Runtime: tether.Runtime.WASM("/static/client.go.wasm"),
	//     }
	Runtime ClientRuntime
}

// defaults fills zero-valued fields with sensible defaults.
func (c *Client) defaults() {
	if c.DefaultDebounce == 0 {
		c.DefaultDebounce = defaultDefaultDebounce
	}
	if c.TransitionTimeout == 0 {
		c.TransitionTimeout = defaultTransitionTimeout
	}
	if c.FlashDuration == 0 {
		c.FlashDuration = defaultFlashDuration
	}
	if c.ToastDuration == 0 {
		c.ToastDuration = defaultToastDuration
	}
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
	// session reconnect entirely. When true, [SessionMatch] is
	// ignored and possession of the session ID alone is enough to
	// reconnect.
	//
	// Note that the User-Agent check is a tripwire, not real theft
	// protection - an attacker who has stolen a session ID can
	// usually spoof the UA too. The real protections are keeping the
	// ID out of URLs and logs (the framework carries it in headers
	// and one-time connect tickets) and TLS. The UA check merely
	// stops casual replay of a leaked ID from a different device.
	DisableSessionBinding bool

	// SessionMatch customises how the framework compares
	// User-Agent strings on reconnect. The function receives the
	// original UA (captured at session creation) and the
	// reconnecting client's UA. Return true to allow the
	// reconnect, false to reject it.
	//
	// When nil (the default), the framework performs an exact
	// string match. This is the strictest and safest option.
	//
	// Set this when exact matching is too strict for your
	// deployment - for example, when browser auto-updates change
	// the UA version during long-lived frozen sessions:
	//
	//	Security: tether.Security{
	//	    SessionMatch: func(original, reconnect string) bool {
	//	        return extractBrowser(original) == extractBrowser(reconnect)
	//	    },
	//	}
	//
	// Ignored when [DisableSessionBinding] is true.
	SessionMatch func(original, reconnect string) bool
}

// matchUA reports whether the reconnecting client's User-Agent is
// acceptable for the given original UA. Returns true when session
// binding is disabled, when the custom matcher accepts the pair, or
// when no custom matcher is set and the strings match exactly.
func (s Security) matchUA(original, reconnect string) bool {
	if s.DisableSessionBinding {
		return true
	}
	if s.SessionMatch != nil {
		return s.SessionMatch(original, reconnect)
	}
	return original == reconnect
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

// defaultPendingCheckInterval is how often the background goroutine
// scans for expired pending sessions.
const defaultPendingCheckInterval = 10 * time.Second

// defaultMaxNavigateRedirects caps inline server-side redirects per
// navigate event.
const defaultMaxNavigateRedirects = 5

const (
	defaultRetryDelay        = 500 * time.Millisecond
	defaultMaxRetryDelay     = 10 * time.Second
	defaultBackoffMultiplier = 1.5
	defaultDefaultDebounce   = 300 * time.Millisecond
	defaultTransitionTimeout = 5 * time.Second
	defaultFlashDuration     = 5 * time.Second
	defaultToastDuration     = 5 * time.Second
	defaultSyncRetention     = 1 * time.Hour
)

// logAppliedDefaults logs which configuration fields were left at zero
// and filled with framework defaults. Only fires in dev mode so
// production remains quiet. Helps developers understand which values
// the framework chose on their behalf when debugging timeout or
// capacity issues.
func logAppliedDefaults(t Timeouts, l Limits, app App) {
	if !dev.Enabled() {
		return
	}

	var defaults []any
	add := func(name string, value any) {
		defaults = append(defaults, name, value)
	}

	// Timeouts that were defaulted (already filled by caller).
	if t.Reconnect == defaultReconnectTimeout {
		add("Timeouts.Reconnect", t.Reconnect)
	}
	if t.Pending == defaultPendingTimeout {
		add("Timeouts.Pending", t.Pending)
	}
	if t.Heartbeat == defaultHeartbeatInterval {
		add("Timeouts.Heartbeat", t.Heartbeat)
	}
	if t.Retry == defaultRetryDelay {
		add("Timeouts.Retry", t.Retry)
	}
	if t.MaxRetry == defaultMaxRetryDelay {
		add("Timeouts.MaxRetry", t.MaxRetry)
	}
	if t.BackoffMultiplier == defaultBackoffMultiplier {
		add("Timeouts.BackoffMultiplier", t.BackoffMultiplier)
	}
	if t.PendingCheck == defaultPendingCheckInterval {
		add("Timeouts.PendingCheck", t.PendingCheck)
	}

	// Limits that were defaulted.
	if l.MaxEventBytes == defaultMaxEventBytes {
		add("Limits.MaxEventBytes", l.MaxEventBytes)
	}
	if l.MaxPushSubscriptionBytes == defaultMaxPushSubscriptionBytes {
		add("Limits.MaxPushSubscriptionBytes", l.MaxPushSubscriptionBytes)
	}
	if l.CmdBufferSize == defaultCmdBufferSize {
		add("Limits.CmdBufferSize", l.CmdBufferSize)
	}
	if l.MaxNavigateRedirects == defaultMaxNavigateRedirects {
		add("Limits.MaxNavigateRedirects", l.MaxNavigateRedirects)
	}

	// App-level defaults.
	if app.MaxPending == defaultMaxPending {
		add("App.MaxPending", app.MaxPending)
	}
	if app.ShutdownGrace == defaultShutdownGrace {
		add("App.ShutdownGrace", app.ShutdownGrace)
	}

	if len(defaults) > 0 {
		dev.Debug("applied default configuration", defaults...)
	}
}
