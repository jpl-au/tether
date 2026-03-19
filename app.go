package tether

import "log/slog"

// App holds configuration shared across all handlers in an
// application: logging, client-side behaviour, security, and
// assets. Create one App and pass it to [Stateful] and [Stateless] — each
// handler gets its own copy, so shared settings are defined once.
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
	// sets it as the process-wide slog default once. When provided,
	// the framework uses it without touching the global default.
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
}
