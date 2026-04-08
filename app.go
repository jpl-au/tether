package tether

import (
	"log/slog"
	"net/http"
	"time"
)

// App holds configuration shared across all handlers in an
// application: logging, client-side behaviour, security, assets,
// and transports. Create one App and pass it to [Stateful] and
// [Stateless] - each handler gets its own copy, so shared settings
// are defined once.
//
// The zero value provides sensible defaults: WebSocket as the
// primary transport and SSE as the fallback. Override Upgrade
// and/or Fallback to customise transport options.
//
//	app := tether.App{
//	    DevMode: true,
//	    Assets:  []*tether.Asset{assets},
//	}
//
//	live := tether.Stateful(app, tether.StatefulConfig[State]{...})
//	page := tether.Stateless(app, tether.StatelessConfig[State]{...})
//
// # Value semantics
//
// App is passed by value. Each handler receives an independent copy
// so settings cannot be mutated from outside after construction.
// Internally the handler configures the [dev] package's process-wide
// logger and dev-mode flag. This global state is intentional: dev
// mode is a property of the development session, not of individual
// handlers. See the [dev] package documentation for the full
// rationale.
//
// # Observability
//
// App.Logger configures the [dev] package's scoped logger, which is a
// development debugging aid (verbose, human-readable, gated behind
// DevMode). For production observability, subscribe to
// [Handler].Diagnostics which provides typed, per-handler events for
// metrics, alerting, and structured logging. See [Diagnostic] and
// [Bus] for details.
type App struct {
	// DevMode enables development conveniences: debug logging by
	// default, Cache-Control: no-store on all responses, service
	// worker unregistration, and the Tether.disconnect() test hook
	// in the client JS. Enable via this field or set the TETHER_DEV
	// environment variable to any non-empty value.
	DevMode bool

	// Logger used for framework log output. When nil, the framework
	// creates a text handler at INFO level (DEBUG in DevMode) and
	// configures the dev package's scoped logger. The process-wide
	// slog default is NEVER modified - tether's logger is scoped
	// to the framework. When provided, the framework uses it
	// without touching the global default.
	Logger *slog.Logger

	// Client groups browser-side settings passed to the client JS
	// as data attributes on the tether root element.
	Client Client

	// Security groups origin-checking and CSRF protection settings.
	Security Security

	// Assets lists embedded asset collections to auto-serve. Each
	// [Asset] provides content-hashed URLs for cache-busting.
	// Assets are served at their configured prefix (default
	// "/assets/") with appropriate cache headers.
	Assets []*Asset

	// Upgrade is the primary transport upgrade function for stateful
	// handlers. Defaults to ws.Upgrade() (WebSocket) when nil.
	// Per-handler overrides on [StatefulConfig] take precedence.
	Upgrade func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// Fallback is the secondary transport upgrade function for
	// stateful handlers. Defaults to sse.Upgrade() (Server-Sent
	// Events) when nil. Per-handler overrides on [StatefulConfig]
	// take precedence.
	Fallback func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// ShutdownGrace is how long [ListenAndServe] and
	// [Handler.ListenAndServe] wait for sessions to drain during
	// graceful shutdown. After this period, remaining sessions are
	// force-closed. Also used as the TTL when persisting session
	// state to the [SessionStore] during shutdown. Zero defaults to
	// 10 seconds.
	ShutdownGrace time.Duration

	// MaxSessions limits the total number of concurrent sessions
	// (pending + active + disconnected) across all handlers. Zero
	// means unlimited. In production, set a limit to prevent
	// resource exhaustion.
	MaxSessions int

	// MaxPending limits the number of pre-warmed sessions waiting
	// for a browser to open a transport connection. Each GET request
	// creates a pending session (state + differ), so this cap
	// protects against GET-flooding attacks where an attacker scripts
	// thousands of requests without ever connecting. Pending sessions
	// are cheap but unauthenticated - capping them separately
	// prevents an attacker from crowding out legitimate active
	// sessions under the global MaxSessions limit. Zero defaults to
	// 128.
	MaxPending int

	// Cluster enables cross-node communication for [Bus] and [Value].
	// When set, any Bus or Value created with a topic name publishes
	// state changes to the cluster and subscribes to changes from
	// other nodes. See [Cluster] for the interface contract.
	Cluster Cluster
}
