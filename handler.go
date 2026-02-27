package poly

import (
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent-poly/mode"
)

// pendingSession holds a pre-warmed session created during the initial GET
// request. The state and differ are seeded so that the WebSocket can attach
// without repeating the initial render.
type pendingSession[S any] struct {
	state     S
	differ    *jit.Differ
	createdAt time.Time
}

// defaultPendingTimeout is used when PendingTimeout is zero.
const defaultPendingTimeout = 30 * time.Second

// Handler manages the lifecycle of poly sessions. Sessions move through
// three pools — pending, active, and disconnected — so the server can
// pre-warm state on the initial GET and preserve it across brief network
// interruptions. Use Shutdown for graceful termination.
//
// The handler also serves the embedded client runtime at /_poly/ — there
// is no need to mount a separate file server for the JS assets.
type Handler[S any] struct {
	cfg          Config[S]
	mu           sync.Mutex
	pending      map[string]*pendingSession[S]
	active       map[string]*Session[S]
	disconnected map[string]*Session[S]
	done         chan struct{}
	closeOnce    sync.Once
	draining     atomic.Bool

	// clientHandler serves the embedded JS runtime at /_poly/*.
	clientHandler http.Handler

	// assetMounts serves embedded application assets at their
	// configured URL prefixes, one per [Asset] in Config.Assets.
	assetMounts []assetMount
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
		panic("poly: Config.InitialState is required")
	}
	if cfg.Render == nil {
		panic("poly: Config.Render is required")
	}
	if cfg.Handle == nil {
		panic("poly: Config.Handle is required")
	}
	if cfg.Mode != mode.SSE && cfg.Upgrade == nil {
		panic("poly: Config.Upgrade is required — use ws.Upgrade() or set Mode to mode.SSE")
	}
	if cfg.Mode != mode.WebSocket && cfg.Fallback == nil {
		panic("poly: Config.Fallback is required — use sse.Upgrade() or set Mode to mode.WebSocket")
	}

	if len(cfg.Middleware) > 0 {
		cfg.Handle = chain(cfg.Handle, cfg.Middleware)
	}

	if cfg.Push != nil {
		cfg.Worker = true
	}
	if !cfg.DevMode && os.Getenv("POLY_DEV") != "" {
		cfg.DevMode = true
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Timeouts.Reconnect == 0 {
		cfg.Timeouts.Reconnect = defaultReconnectTimeout
	}
	if cfg.Limits.MaxEventBytes == 0 {
		cfg.Limits.MaxEventBytes = defaultMaxEventBytes
	}
	if cfg.Timeouts.Pending == 0 {
		cfg.Timeouts.Pending = defaultPendingTimeout
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
	if cfg.Timeouts.Heartbeat == 0 {
		cfg.Timeouts.Heartbeat = defaultHeartbeatInterval
	}
	if cfg.Limits.CmdBufferSize == 0 {
		cfg.Limits.CmdBufferSize = defaultCmdBufferSize
	}
	mounts := buildAssetMounts(cfg.Assets, cfg.DevMode)

	h := &Handler[S]{
		cfg:           cfg,
		pending:       make(map[string]*pendingSession[S]),
		active:        make(map[string]*Session[S]),
		disconnected:  make(map[string]*Session[S]),
		done:          make(chan struct{}),
		clientHandler: newClientHandler(cfg.Assets),
		assetMounts:   mounts,
	}

	go h.reapPending()

	return h
}

// destroySession performs permanent cleanup for a session that is no
// longer reachable (reaped, shutdown, or disconnected with timeout -1).
// Cancelling the context causes the session loop to exit.
func (h *Handler[S]) destroySession(s *Session[S]) {
	if s.stop != nil {
		s.stop()
	}

	for _, g := range h.cfg.Groups {
		g.Remove(s)
	}
}
