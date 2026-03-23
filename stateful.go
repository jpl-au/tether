package tether

import (
	"os"
	"reflect"
	"runtime"
	"strings"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/protocol"
	"github.com/jpl-au/tether/wire"
)

// Stateful creates a [Handler] that maintains a persistent connection
// (WebSocket or SSE) between browser and server. State survives
// across interactions - when the user triggers an event, the server
// updates state and pushes the change without a page reload.
//
// Use Stateful for interactive applications: dashboards, forms, chat,
// real-time collaboration - anything where the server needs to push
// updates or maintain session state between interactions.
//
// For traditional request/response pages that reconstruct state from
// each HTTP request, use [Stateless] instead.
//
// Session lifecycle is managed by per-session timers (idle, lifetime,
// disconnect) - there is no centralised reaper goroutine. A
// lightweight pending-cleanup goroutine removes pre-warmed sessions
// that are never claimed.
//
// Call [Handler.Shutdown] to cancel all sessions before the process
// exits.
func Stateful[S any](app App, cfg StatefulConfig[S]) *Handler[S] {
	if cfg.InitialState == nil {
		panic("tether: StatefulConfig.InitialState is required")
	}
	if cfg.Render == nil {
		panic("tether: StatefulConfig.Render is required")
	}
	if cfg.Handle == nil {
		panic("tether: StatefulConfig.Handle is required")
	}
	if cfg.Mode == mode.HTTP {
		panic("tether: mode.HTTP is for tether.Page - use mode.WebSocket, mode.ServerSentEvents, or mode.Both")
	}
	if cfg.Mode == 0 {
		cfg.Mode = mode.Both
	}
	if cfg.Mode != mode.ServerSentEvents && cfg.Upgrade == nil {
		panic("tether: StatefulConfig.Upgrade is required - use ws.Upgrade() or set Mode to mode.ServerSentEvents")
	}
	if cfg.Mode != mode.WebSocket && cfg.Fallback == nil {
		panic("tether: StatefulConfig.Fallback is required - use sse.Upgrade() or set Mode to mode.WebSocket")
	}
	// Compose component routing into Handle so that mounted component
	// events flow through middleware. Without this, component events
	// would bypass middleware entirely.
	if len(cfg.Components) > 0 {
		appHandle := cfg.Handle
		components := cfg.Components
		cfg.Handle = func(sess Session, s S, ev Event) S {
			if ev.Type != event.Navigate {
				if newState, ok := RouteMount(components, sess, s, ev); ok {
					return newState
				}
			}
			return appHandle(sess, s, ev)
		}
	}

	// Compose OnNavigate into Handle so the middleware chain applies
	// to navigate events. Without this, navigate events bypass
	// middleware entirely because exec dispatches them directly.
	if cfg.OnNavigate != nil {
		appHandle := cfg.Handle
		appNav := cfg.OnNavigate
		cfg.Handle = func(sess Session, s S, ev Event) S {
			if ev.Type == event.Navigate {
				return appNav(sess, s, paramsFromEvent(ev))
			}
			return appHandle(sess, s, ev)
		}
	}

	if len(cfg.Middleware) > 0 {
		cfg.Handle = Chain(cfg.Handle, cfg.Middleware)
	}

	app.initLog()
	if cfg.Protocol == 0 {
		switch os.Getenv("TETHER_PROTO") {
		case "HTTP1":
			cfg.Protocol = protocol.HTTP1
		case "HTTP2":
			cfg.Protocol = protocol.HTTP2
		case "HTTP3":
			cfg.Protocol = protocol.HTTP3
		case "", "AUTO":
			cfg.Protocol = protocol.Auto
		default:
			dev.Log().Warn("tether: unrecognised TETHER_PROTO value - using Auto",
				"value", os.Getenv("TETHER_PROTO"))
			cfg.Protocol = protocol.Auto
		}
	}
	if cfg.Timeouts.Reconnect == 0 {
		cfg.Timeouts.Reconnect = defaultReconnectTimeout
	}
	if cfg.Limits.MaxEventBytes == 0 {
		cfg.Limits.MaxEventBytes = defaultMaxEventBytes
	}
	if cfg.Limits.MaxPushSubscriptionBytes == 0 {
		cfg.Limits.MaxPushSubscriptionBytes = defaultMaxPushSubscriptionBytes
	}
	if cfg.Limits.MaxPending == 0 {
		cfg.Limits.MaxPending = defaultMaxPending
	}
	if cfg.Timeouts.Pending == 0 {
		cfg.Timeouts.Pending = defaultPendingTimeout
	}
	if cfg.Timeouts.ShutdownGrace == 0 {
		cfg.Timeouts.ShutdownGrace = defaultShutdownGrace
	}
	if cfg.Timeouts.Retry == 0 {
		cfg.Timeouts.Retry = defaultRetryDelay
	}
	if cfg.Timeouts.MaxRetry == 0 {
		cfg.Timeouts.MaxRetry = defaultMaxRetryDelay
	}
	if cfg.Timeouts.BackoffMultiplier < 1 {
		cfg.Timeouts.BackoffMultiplier = defaultBackoffMultiplier
	}
	app.Client.defaults()
	if app.Client.SyncRetention == 0 {
		app.Client.SyncRetention = defaultSyncRetention
	}
	if cfg.Timeouts.Heartbeat == 0 {
		cfg.Timeouts.Heartbeat = defaultHeartbeatInterval
	}
	if cfg.Limits.CmdBufferSize == 0 {
		cfg.Limits.CmdBufferSize = defaultCmdBufferSize
	}

	// Validate boundaries now that defaults are applied.
	if cfg.Timeouts.Retry > cfg.Timeouts.MaxRetry {
		panic("tether: Timeouts.Retry must not exceed Timeouts.MaxRetry")
	}
	if cfg.Timeouts.BackoffMultiplier > 10 {
		panic("tether: Timeouts.BackoffMultiplier must not exceed 10")
	}

	if cfg.Limits.MaxSessions == 0 && !app.DevMode {
		dev.Log().Warn("tether: Limits.MaxSessions is 0 (unlimited) - set a limit in production to prevent resource exhaustion")
	}
	if cfg.Freeze != 0 {
		if cfg.SessionStore == nil {
			panic("tether: Freeze requires a SessionStore")
		}
		if cfg.Freeze == FreezeWithRestore && cfg.OnRestore == nil {
			panic("tether: FreezeWithRestore requires OnRestore - implement OnRestore to re-fetch state, or use FreezeWithConnect to fall back to OnConnect")
		}
	}
	mounts := app.mountAssets()
	csrf := app.Security.csrf()

	h := &Handler[S]{
		app:           app,
		cfg:           cfg,
		pending:       make(map[string]*pendingSession[S]),
		active:        make(map[string]*StatefulSession[S]),
		disconnected:  make(map[string]*StatefulSession[S]),
		done:          make(chan struct{}),
		drainNotify:   make(chan struct{}, 1),
		encoder:       resolveEncoder(cfg.WireFormat),
		clientHandler: app.jsHandler(),
		assetMounts:   mounts,
		csrf:          csrf,
		Diagnostics:   NewBus[Diagnostic](),
	}

	go h.reapPending()

	dev.Log().Info("tether: ready", handlerAttrs(app, cfg)...)

	return h
}

// resolveEncoder returns an Encoder for the given wire format. JSON is
// the default and currently the only supported format.
func resolveEncoder(f wire.Format) wire.Encoder {
	switch f {
	default:
		return wire.JSONEncoder{}
	}
}

// handlerAttrs builds the slog attribute list for the "tether: ready"
// startup log. Transport is always present; name, worker, middleware,
// and dev are included only when set, to keep the line uncluttered.
func handlerAttrs[S any](app App, cfg StatefulConfig[S]) []any {
	args := []any{"transport", transportLabel(cfg.Mode), "protocol", cfg.Protocol.String()}
	if cfg.Name != "" {
		args = append(args, "name", cfg.Name)
	}
	if cfg.Worker {
		args = append(args, "worker", true)
	}
	if len(cfg.Middleware) > 0 {
		args = append(args, "middleware", middlewareNames(cfg.Middleware))
	}
	if app.DevMode {
		args = append(args, "dev", true)
	}
	return args
}

// middlewareNames derives a display name for each middleware function by
// reflecting on its program counter once at construction time. Named
// functions produce clean short names (e.g. "withAuth"); anonymous
// closures produce "funcN", which is an honest signal to name them.
func middlewareNames[S any](mws []Middleware[S]) []string {
	names := make([]string, len(mws))
	for i, mw := range mws {
		pc := reflect.ValueOf(mw).Pointer()
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			names[i] = "unknown"
			continue
		}
		name := fn.Name()
		// Trim generic suffix before splitting on package path, otherwise
		// LastIndex picks up dots inside the type parameter list and gives
		// us a fragment like "State].func1" instead of the function name.
		if j := strings.Index(name, "["); j >= 0 {
			name = name[:j]
		}
		// Trim package path: "github.com/example/app.withAuth" → "withAuth"
		if j := strings.LastIndex(name, "."); j >= 0 {
			name = name[j+1:]
		}
		names[i] = name
	}
	return names
}

// transportLabel returns a human-readable label for a mode.Transport
// value. The label appears in the "tether: ready" startup log so
// developers can distinguish handlers at a glance.
func transportLabel(m mode.Transport) string {
	switch m {
	case mode.WebSocket:
		return "ws"
	case mode.ServerSentEvents:
		return "sse"
	case mode.Both:
		return "ws+sse"
	default:
		return "http"
	}
}
