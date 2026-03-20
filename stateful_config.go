package tether

import (
	"context"
	"net/http"

	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/protocol"
	"github.com/jpl-au/tether/push"
	"github.com/jpl-au/tether/wire"
)

// StatefulConfig wires together all the pieces of a stateful page:
// how to create initial state, how to render it, and how to handle
// events. The type parameter S is the session state - typically a
// struct, but it can be any type. Each connected browser tab gets its
// own independent copy of S, so state is never shared across sessions
// unless you explicitly coordinate via [Group] or external storage.
//
// A stateful page maintains a persistent connection (WebSocket or SSE)
// between browser and server. State survives across interactions and
// the server can push updates at any time. For traditional
// request/response pages, use [StatelessConfig] with [Stateless]
// instead.
//
// At minimum, set InitialState, Render, Handle, and either Upgrade or
// Fallback (depending on Mode). Everything else is optional and has
// sensible defaults.
type StatefulConfig[S any] struct {
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

	// Protocol sets the HTTP protocol the server uses. When set to
	// [protocol.Auto] (the default), the framework detects the
	// protocol from each request. Set explicitly when you know your
	// environment - e.g. [protocol.HTTP2] when serving HTTPS
	// directly, or [protocol.HTTP1] behind a downgrading proxy.
	// Mismatches between the configured and detected protocol emit
	// a warning on every affected request.
	//
	// Can also be set via the TETHER_PROTO environment variable
	// (HTTP1, HTTP2, HTTP3, AUTO). Explicit config takes precedence.
	//
	// Protocol awareness applies to stateful sessions only - [Stateless] is
	// stateless and does not benefit from protocol-specific behaviour.
	Protocol protocol.Protocol

	// InitialState returns the starting state for a new session.
	// Called once per connection to create the initial state.
	InitialState func(r *http.Request) S

	// Render builds a node tree from the current state.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state. Side
	// effects (toast, navigate, title, etc.) are expressed as imperative
	// calls on the session parameter. In stateful mode the session is a
	// [*StatefulSession] which can be type-asserted for Update, Go, and Close.
	// See [HandleFunc] for concurrency constraints - Handle runs inside
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
	// The session parameter is a [Session] because this function
	// runs both during pre-warming (initial GET, before a real session
	// exists) and during live navigation. Side-effect methods (SetTitle,
	// Toast, etc.) are always safe to call. During pre-warming, effects
	// are captured; during navigation, they are sent to the client.
	OnNavigate func(session Session, state S, params Params) S

	// OnConnect is called after a new session is created, its transport
	// is ready, and any [StatefulConfig.Watchers] have been subscribed. Use
	// this for imperative setup: incrementing counters, publishing
	// events, starting background goroutines, or logging. For reactive
	// subscriptions, prefer [StatefulConfig.Watchers] which are declarative
	// and visible on StatefulConfig. Optional.
	//
	// OnConnect runs on the HTTP handler goroutine after the session's
	// command loop has started but before the transport begins reading
	// client events. This means State, Update, On, Observe, and all
	// side-effect methods are safe to call. However, any blocking work
	// (slow database queries, HTTP calls) delays the session becoming
	// fully interactive - move heavy initialisation into [StatefulSession.Go].
	OnConnect func(session *StatefulSession[S])

	// OnDisconnect is called after a session's transport closes (either
	// because the client disconnected or the session was reaped). Use
	// this to remove the session from a [Group] and clean up any
	// resources started in OnConnect. Optional.
	OnDisconnect func(session *StatefulSession[S])

	// Equal compares two states. When provided and the old and new state
	// are equal, the render and diff are skipped entirely - no work is
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
	// The callback runs inside the session's command loop - keep it
	// fast and offload any expensive work to a goroutine. Optional.
	OnStructuralChange func(session *StatefulSession[S], change StructuralChange)

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
	// The callback runs inside the session's command loop - keep it
	// fast and offload any expensive work to a goroutine. Optional.
	OnNoPatch func(session *StatefulSession[S], info NoPatch)

	// Layout wraps the tether content in a full HTML document. The state
	// parameter is the session's initial state, which can be used to set
	// the page title or other document-level elements. The content
	// parameter is a node that renders the tether root div and client
	// scripts. Return a complete document tree (e.g.
	// html.New(head.New(...), body.New(content))).
	//
	// Layout runs once on the initial GET request. After that, only the
	// tether root div is morphed - the outer shell is not re-rendered.
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

	// Name identifies this handler in log output. When multiple handlers
	// share the same transport, a name distinguishes their "tether: ready"
	// lines and any other structured log output that includes it. Optional.
	Name string

	// Worker enables the full service worker for asset caching, offline
	// page shells, and background sync. When true, the client JS
	// registers /_tether/tether-worker.js as a service worker with
	// scope "/". When false and Push is configured, a lightweight
	// push-only service worker is registered instead - it handles push
	// events without intercepting fetch requests or caching. Default
	// false.
	Worker bool

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
	// permanently destroyed. Using Groups on StatefulConfig avoids repetitive
	// Add/Remove boilerplate in OnConnect/OnDisconnect. Optional.
	Groups []*Group[S]

	// Watchers are reactive sources that sessions automatically
	// subscribe to when connected. Each watcher maps external changes
	// into the session's state. Watchers are subscribed before
	// [StatefulConfig.OnConnect] runs, so the session receives updates from
	// the moment it connects. Create watchers with [WatchValue] and
	// [WatchBus]. Optional.
	Watchers []Watcher[S]

	// Components declares component mounts for automatic event routing.
	// Before the session's [HandleFunc] runs, each event is checked
	// against the mounted components. If a mount's prefix matches the
	// event action, the component handles the event and the user's
	// Handle function never sees it. Create mounts with [Mount].
	//
	// This follows the same declarative pattern as [StatefulConfig.Watchers]
	// and [StatefulConfig.Groups]: wired once at StatefulConfig time, automatically
	// managed by the framework.
	Components []ComponentMount[S]

	// Timeouts groups all duration-based settings that control session
	// lifecycle, reconnection, and transport keep-alive timing.
	Timeouts Timeouts

	// Limits groups capacity constraints: session counts, channel
	// buffer sizes, and request body limits.
	Limits Limits

	// WireFormat selects the encoding for server-to-client updates.
	// Defaults to [wire.JSON]. Currently the only supported format;
	// additional formats (e.g. HTML fragments) will be added in future.
	WireFormat wire.Format

	// DiffStore provides external persistence for disconnected session
	// snapshots. When set, differ data is saved to the store on
	// disconnect and deleted on reconnect (Render re-seeds the
	// differ), freeing Go memory during the reconnect window. When
	// nil (default), snapshots remain in process memory.
	DiffStore DiffStore

	// SessionStore provides external persistence for session state
	// S, enabling crash recovery and node migration. When set, the
	// framework saves state on disconnect and graceful shutdown, and
	// restores it when a reconnecting client reaches a server with
	// no in-memory session. When nil (default), sessions live
	// entirely in memory. See [SessionStore] for the interface
	// contract.
	SessionStore SessionStore

	// Codec controls how session state S is serialised for external
	// storage. When nil, the framework uses CBOR encoding (RFC 8949)
	// which handles any struct with exported fields. Implement
	// [SessionCodec] when you need encryption, a specific wire
	// format, or custom handling of complex types. Only used when
	// SessionStore is set.
	Codec SessionCodec[S]

	// OnRestore is called when a session is restored from external
	// storage (crash recovery or node migration). The session's
	// state S has been deserialised and is available via State().
	// Use this to re-establish runtime resources: rejoin groups,
	// restart timers, re-subscribe to buses.
	//
	// OnRestore fires instead of OnConnect for restored sessions.
	// If nil, OnConnect fires as a fallback - suitable for apps
	// where setup is identical for new and restored sessions.
	OnRestore func(session *StatefulSession[S])

	// FreezeOnDisconnect enables frozen mode for disconnected
	// sessions. When true, a session that loses its transport
	// persists state S to the [SessionStore], releases the state
	// and differ from memory, and exits the command loop. The
	// session becomes a lightweight stub holding only its ID and
	// metadata. On reconnect, the framework loads state from the
	// store, starts a fresh loop, and fires [OnRestore].
	//
	// This dramatically reduces memory for disconnected sessions
	// at the cost of commands (Update, broadcasts, timer callbacks)
	// being silently discarded while frozen. Enable this when
	// sessions do not need background processing during disconnect.
	//
	// Requires [SessionStore] to be configured. If SessionStore is
	// nil and FreezeOnDisconnect is true, the framework logs a
	// warning at startup and disables freeze.
	FreezeOnDisconnect bool
}

// PushConfig enables Web Push notifications for the page. The VAPID
// public key is passed to the client so it can subscribe when the user
// clicks a [bind.PushSubscribe] element. Subscription is never
// automatic - it always requires a user gesture.
type PushConfig[S any] struct {
	// Sender handles push notification delivery. Create with
	// [push.NewSender]. The sender's public key is automatically
	// used for client-side push subscription.
	Sender *push.Sender

	// OnSubscribe is called when a client sends its push subscription
	// to the server. Store the subscription to send notifications later
	// via [push.Sender.Send]. The callback runs in its own goroutine
	// so it is safe to perform I/O (e.g. database writes).
	//
	// The context is derived from the session and cancels when the
	// session is destroyed - use it for database calls and external
	// requests to avoid leaking goroutines. The subscription is passed
	// as a parameter; do not read it from the session object as the
	// store may not have completed yet. Optional.
	OnSubscribe func(ctx context.Context, session *StatefulSession[S], sub push.Subscription)
}
