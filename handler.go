package poly

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	jit "github.com/jpl-au/fluent-jit"
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
type Handler[S any] struct {
	cfg          Config[S]
	mu           sync.Mutex
	pending      map[string]*pendingSession[S]
	active       map[string]*Session[S]
	disconnected map[string]*Session[S]
	done         chan struct{}
	closeOnce    sync.Once
	draining     atomic.Bool
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
	if cfg.Mode != SSEOnly && cfg.Upgrade == nil {
		panic("poly: Config.Upgrade is required for WebSocket mode")
	}
	if cfg.Mode != WebSocketOnly && cfg.Fallback == nil {
		panic("poly: Config.Fallback is required for SSE mode")
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
	if cfg.ReconnectTimeout == 0 {
		cfg.ReconnectTimeout = defaultReconnectTimeout
	}
	if cfg.MaxEventBytes == 0 {
		cfg.MaxEventBytes = defaultMaxEventBytes
	}
	if cfg.PendingTimeout == 0 {
		cfg.PendingTimeout = defaultPendingTimeout
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = defaultRetryDelay
	}
	if cfg.MaxRetryDelay == 0 {
		cfg.MaxRetryDelay = defaultMaxRetryDelay
	}
	if cfg.DefaultDebounce == 0 {
		cfg.DefaultDebounce = defaultDefaultDebounce
	}
	if cfg.TransitionTimeout == 0 {
		cfg.TransitionTimeout = defaultTransitionTimeout
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
	}
	h := &Handler[S]{
		cfg:          cfg,
		pending:      make(map[string]*pendingSession[S]),
		active:       make(map[string]*Session[S]),
		disconnected: make(map[string]*Session[S]),
		done:         make(chan struct{}),
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
