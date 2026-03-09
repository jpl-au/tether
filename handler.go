package tether

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent-tether/dev"
	"github.com/jpl-au/fluent-tether/event"
	"github.com/jpl-au/fluent-tether/mode"
	"github.com/jpl-au/fluent-tether/wire"
)

// pendingSession holds a pre-warmed session created during the initial GET
// request. The state and differ are seeded so that the WebSocket can attach
// without repeating the initial render.
type pendingSession[S any] struct {
	state     S
	differ    *jit.Differ
	createdAt time.Time
	userAgent string
}

// defaultPendingTimeout is used when PendingTimeout is zero.
const defaultPendingTimeout = 30 * time.Second

// Handler manages the lifecycle of tether sessions. Sessions move through
// three pools — pending, active, and disconnected — so the server can
// pre-warm state on the initial GET and preserve it across brief network
// interruptions. Use Shutdown for graceful termination.
//
// The handler also serves the embedded client runtime at /_tether/ — there
// is no need to mount a separate file server for the JS assets.
type Handler[S any] struct {
	cfg          Config[S]
	mu           sync.Mutex
	pending      map[string]*pendingSession[S]
	active       map[string]*LiveSession[S]
	disconnected map[string]*LiveSession[S]
	done         chan struct{}
	closeOnce    sync.Once
	draining     atomic.Bool

	// clientHandler serves the embedded JS runtime at /_tether/*.
	clientHandler http.Handler

	// encoder serialises updates for the wire format selected by
	// Config.WireFormat. All sessions inherit this encoder.
	encoder wire.Encoder

	// assetMounts serves embedded application assets at their
	// configured URL prefixes, one per [Asset] in Config.Assets.
	assetMounts []assetMount

	// Diagnostics emits framework-level events so application code
	// can observe them for metrics, alerting, or custom logging.
	// The framework is quiet by default — slog is only used for
	// panics. All other operational signals (transport errors,
	// encode failures, buffer overflows, upload errors) flow
	// exclusively through this bus.
	//
	// Subscribe with [Bus.Subscribe] (synchronous) or
	// [Bus.SubscribeAsync] (own goroutine per event, safe for I/O):
	//
	//	h.Diagnostics.Subscribe(ctx, func(d tether.Diagnostic) {
	//	    metrics.Inc("tether." + string(d.Kind))
	//	})
	//
	//	h.Diagnostics.SubscribeAsync(ctx, func(d tether.Diagnostic) {
	//	    if d.Kind == tether.HandlerPanic {
	//	        alerting.Critical(d.SessionID, d.Err)
	//	    }
	//	})
	Diagnostics *Bus[Diagnostic]
}

// assetMount pairs a URL prefix with a handler that serves files from
// the corresponding [Asset] filesystem.
type assetMount struct {
	prefix  string
	handler http.Handler
}

// New creates a [Handler] from the given configuration. Session
// lifecycle is managed by per-session timers (idle, lifetime,
// disconnect) — there is no centralised reaper goroutine. A
// lightweight pending-cleanup goroutine removes pre-warmed sessions
// that are never claimed.
//
// Call [Handler.Shutdown] to cancel all sessions before the process
// exits.
func New[S any](cfg Config[S]) *Handler[S] {
	if cfg.InitialState == nil {
		panic("tether: Config.InitialState is required")
	}
	if cfg.Render == nil {
		panic("tether: Config.Render is required")
	}
	if cfg.Handle == nil {
		panic("tether: Config.Handle is required")
	}
	if cfg.Mode == mode.HTTP {
		panic("tether: mode.HTTP is for tether.Page — use mode.WebSocket, mode.ServerSentEvents, or mode.Both")
	}
	if cfg.Mode == 0 {
		cfg.Mode = mode.Both
	}
	if cfg.Mode != mode.ServerSentEvents && cfg.Upgrade == nil {
		panic("tether: Config.Upgrade is required — use ws.Upgrade() or set Mode to mode.ServerSentEvents")
	}
	if cfg.Mode != mode.WebSocket && cfg.Fallback == nil {
		panic("tether: Config.Fallback is required — use sse.Upgrade() or set Mode to mode.WebSocket")
	}
	for _, o := range cfg.Security.AllowedOrigins {
		if o == "" {
			panic("tether: Security.AllowedOrigins contains an empty string — remove it or provide a valid origin like \"https://example.com\"")
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
				params := Params{Path: ev.Data["path"]}
				if search := ev.Data["search"]; search != "" {
					var err error
					params.Query, err = url.ParseQuery(search)
					if err != nil {
						slog.Warn("malformed query string in navigate event", "search", search, "err", err)
					}
				}
				return appNav(sess, s, params)
			}
			return appHandle(sess, s, ev)
		}
	}

	if len(cfg.Middleware) > 0 {
		cfg.Handle = chain(cfg.Handle, cfg.Middleware)
	}

	if !cfg.DevMode && os.Getenv("TETHER_DEV") != "" {
		cfg.DevMode = true
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
	}
	slog.SetDefault(cfg.Logger)
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
	mounts := buildAssetMounts(cfg.Assets)

	h := &Handler[S]{
		cfg:           cfg,
		pending:       make(map[string]*pendingSession[S]),
		active:        make(map[string]*LiveSession[S]),
		disconnected:  make(map[string]*LiveSession[S]),
		done:          make(chan struct{}),
		encoder:       resolveEncoder(cfg.WireFormat),
		clientHandler: newClientHandler(cfg.Assets),
		assetMounts:   mounts,
		Diagnostics:   NewBus[Diagnostic](),
	}

	go h.reapPending()

	cfg.Logger.Info("tether: ready", handlerAttrs(cfg)...)

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
func handlerAttrs[S any](cfg Config[S]) []any {
	args := []any{"transport", transportLabel(cfg.Mode)}
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

// destroySession performs permanent cleanup for a session that is no
// longer reachable (reaped, shutdown, or disconnected with timeout -1).
// Cancelling the context causes the session loop to exit.
func (h *Handler[S]) destroySession(s *LiveSession[S]) {
	if s.stop != nil {
		s.stop()
	}

	// Remove stored snapshots for sessions that were offloaded to
	// the store during disconnect. No-op if nothing was stored.
	if h.cfg.Store != nil {
		if err := h.cfg.Store.Delete(context.Background(), s.id); err != nil {
			dev.Warn("store delete failed on destroy", "session", s.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      StoreError,
				SessionID: s.id,
				Err:       err,
				Detail:    "delete",
			})
		}
	}

	for _, g := range h.cfg.Groups {
		g.Remove(s)
	}
}
