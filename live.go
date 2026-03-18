package tether

import (
	"log/slog"
	"net/http"
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

// Live creates a [Handler] that maintains a persistent connection
// (WebSocket or SSE) between browser and server. State survives
// across interactions — when the user triggers an event, the server
// updates state and pushes the change without a page reload.
//
// Use Live for interactive applications: dashboards, forms, chat,
// real-time collaboration — anything where the server needs to push
// updates or maintain session state between interactions.
//
// For traditional request/response pages that reconstruct state from
// each HTTP request, use [Page] instead.
//
// Session lifecycle is managed by per-session timers (idle, lifetime,
// disconnect) — there is no centralised reaper goroutine. A
// lightweight pending-cleanup goroutine removes pre-warmed sessions
// that are never claimed.
//
// Call [Handler.Shutdown] to cancel all sessions before the process
// exits.
func Live[S any](cfg LiveConfig[S]) *Handler[S] {
	if cfg.InitialState == nil {
		panic("tether: LiveConfig.InitialState is required")
	}
	if cfg.Render == nil {
		panic("tether: LiveConfig.Render is required")
	}
	if cfg.Handle == nil {
		panic("tether: LiveConfig.Handle is required")
	}
	if cfg.Mode == mode.HTTP {
		panic("tether: mode.HTTP is for tether.Page — use mode.WebSocket, mode.ServerSentEvents, or mode.Both")
	}
	if cfg.Mode == 0 {
		cfg.Mode = mode.Both
	}
	if cfg.Mode != mode.ServerSentEvents && cfg.Upgrade == nil {
		panic("tether: LiveConfig.Upgrade is required — use ws.Upgrade() or set Mode to mode.ServerSentEvents")
	}
	if cfg.Mode != mode.WebSocket && cfg.Fallback == nil {
		panic("tether: LiveConfig.Fallback is required — use sse.Upgrade() or set Mode to mode.WebSocket")
	}
	for _, o := range cfg.Security.TrustedOrigins {
		if o == "" {
			panic("tether: Security.TrustedOrigins contains an empty string — remove it or provide a valid origin like \"https://example.com\"")
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

	if !cfg.DevMode && os.Getenv("TETHER_DEV") != "" {
		cfg.DevMode = true
	}
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
			slog.Warn("tether: unrecognised TETHER_PROTO value — using Auto",
				"value", os.Getenv("TETHER_PROTO"))
			cfg.Protocol = protocol.Auto
		}
	}
	if cfg.Logger == nil {
		level := slog.LevelInfo
		if cfg.DevMode {
			level = slog.LevelDebug
		}
		opts := &slog.HandlerOptions{Level: level}
		if cfg.LogJSON {
			cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, opts))
		} else {
			cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, opts))
		}
		// Set the process default once. The first handler without an
		// explicit Logger configures the global; subsequent handlers
		// create their own logger but leave the default alone.
		setDefaultLogger(cfg.Logger)
	}
	if cfg.DevMode {
		dev.Enable()
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
	if cfg.Client.DefaultDebounce == 0 {
		cfg.Client.DefaultDebounce = defaultDefaultDebounce
	}
	if cfg.Client.TransitionTimeout == 0 {
		cfg.Client.TransitionTimeout = defaultTransitionTimeout
	}
	if cfg.Client.FlashDuration == 0 {
		cfg.Client.FlashDuration = defaultFlashDuration
	}
	if cfg.Client.ToastDuration == 0 {
		cfg.Client.ToastDuration = defaultToastDuration
	}
	if cfg.Client.SyncRetention == 0 {
		cfg.Client.SyncRetention = defaultSyncRetention
	}
	if cfg.Timeouts.Heartbeat == 0 {
		cfg.Timeouts.Heartbeat = defaultHeartbeatInterval
	}
	if cfg.Limits.CmdBufferSize == 0 {
		cfg.Limits.CmdBufferSize = defaultCmdBufferSize
	}
	if cfg.Limits.MaxSessions == 0 {
		slog.Warn("tether: Limits.MaxSessions is 0 (unlimited) — set a limit in production to prevent resource exhaustion")
	}
	if cfg.FreezeOnDisconnect && cfg.SessionStore == nil {
		slog.Warn("tether: FreezeOnDisconnect requires a SessionStore — frozen mode disabled because there is nowhere to persist state")
		cfg.FreezeOnDisconnect = false
	}
	mounts := buildAssetMounts(cfg.Assets)

	csrf := http.NewCrossOriginProtection()
	for _, origin := range cfg.Security.TrustedOrigins {
		if err := csrf.AddTrustedOrigin(origin); err != nil {
			panic("tether: invalid TrustedOrigins entry " + origin + ": " + err.Error())
		}
	}

	h := &Handler[S]{
		cfg:           cfg,
		pending:       make(map[string]*pendingSession[S]),
		active:        make(map[string]*LiveSession[S]),
		disconnected:  make(map[string]*LiveSession[S]),
		done:          make(chan struct{}),
		encoder:       resolveEncoder(cfg.WireFormat),
		clientHandler: newClientHandler(cfg.Assets),
		assetMounts:   mounts,
		csrf:          csrf,
		Diagnostics:   NewBus[Diagnostic](),
	}

	go h.reapPending()

	cfg.Logger.Info("tether: ready", handlerAttrs(cfg)...)

	if cfg.DevMode && (cfg.Mode == mode.ServerSentEvents || cfg.Mode == mode.Both) {
		dev.Debug("SSE compression is handled by the reverse proxy (nginx, Caddy, Cloudflare) — tether does not compress SSE streams")
	}

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
func handlerAttrs[S any](cfg LiveConfig[S]) []any {
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
	if cfg.DevMode {
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
