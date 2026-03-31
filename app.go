package tether

import (
	"log/slog"
	"net/http"
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
}
