package poly

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
)

// StructuralChange describes a diff result where the render tree's
// Dynamic key set changed — keys were added, removed, or reordered.
// This forces a full root morph instead of targeted patches. The
// fields mirror [jit.StructuralChange] so callers don't need to import
// the diff engine package.
type StructuralChange struct {
	Added     []string // keys present in the new tree but not the old
	Removed   []string // keys present in the old tree but not the new
	Reordered bool     // same keys, different order
	Bytes     int      // size of the re-rendered HTML sent as a root morph
}

// RenderFunc builds a Fluent node tree from the current state. It is
// called on initial page render, after each client event, and after
// each call to [Session.Update]. The function must be pure — given the
// same state it must always produce the same tree, because the diff
// engine compares consecutive renders to compute patches.
type RenderFunc[S any] func(state S) node.Node

// cmdBufferSize is the capacity of the command channel. When full, the
// sender blocks — providing natural backpressure. Convention over
// configuration: no knob.
const cmdBufferSize = 64

// Session represents a single connected client. Each browser tab gets
// its own Session with independent state, a dedicated diff engine, and
// a command-loop goroutine that serialises all state mutations.
//
// All exported methods are safe to call from any goroutine — including
// from within Handle. The command loop processes them in order; there
// is no mutex and no deadlock risk.
type Session[S any] struct {
	id    string
	state S

	render       RenderFunc[S]
	handle       HandleFunc[S]
	handleParams func(*Session[S], S, Params) S
	differ       *jit.Differ
	transport    Transport
	logger       *slog.Logger

	// Channel pair: events from transport, commands from everything else.
	events chan Event
	cmds   chan func()

	// Session lifetime — cancelled on permanent destruction.
	ctx  context.Context
	stop context.CancelFunc

	// Timestamps. lastActivity is atomic so the idle timer reset
	// (inside the loop) and external readers (Health) don't conflict.
	lastActivity atomic.Int64 // UnixNano
	createdAt    time.Time

	// Active during exec() — enables the dual-path pattern.
	handling bool
	fx       *effects

	// Lifecycle timers (replace centralised reaper).
	idleTimer   *time.Timer
	idleTimeout time.Duration
	// disconnectTimer is started when the transport closes and
	// stopped on reattach. If it fires, the session is destroyed.
	disconnectTimer  *time.Timer
	reconnectTimeout time.Duration

	// Push subscription — accessed only from within the loop.
	pushSub *PushSubscription

	// Extension point: called at the end of exec() after the client
	// update is sent. Task 2 (EDD) will use this to publish domain
	// events. Nil until the event bus is wired in.
	afterExec func(trigger Event)

	// Installed by the Handler. Called when the transport reader
	// goroutine exits. Handles pool transitions
	// (active → disconnected or destroy).
	onDisconnect func()

	// Optional equality check — skip render when state unchanged.
	equal func(a, b S) bool

	// Optional telemetry hook for structural diff changes.
	onStructuralChange func(*Session[S], StructuralChange)
}

// ID returns the unique session identifier. This is a cryptographically
// random string generated when the session is created. It can be used
// for logging, metrics, or as a key in external storage.
func (s *Session[S]) ID() string {
	return s.id
}

// Context returns a context that is cancelled when the session is
// permanently destroyed (reaped or shutdown). The context survives
// temporary disconnects and reconnects — use it for background
// goroutines that should keep running while the client is away.
func (s *Session[S]) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// Go launches a goroutine bound to the session's lifetime. The
// context passed to fn is cancelled when the session is permanently
// destroyed (reaped or shutdown). Use this in OnConnect for background
// work like tickers, watchers, or change listeners that should stop
// when the session is gone.
func (s *Session[S]) Go(fn func(ctx context.Context)) {
	go fn(s.Context())
}

// enqueue pushes a command to the session's loop. To prevent
// cross-session deadlocks (e.g. during a Broadcast storm), it uses a
// non-blocking send with a goroutine fallback when the command buffer
// is full. This effectively creates an unbounded mailbox using
// goroutines as the "overflow" buffer.
func (s *Session[S]) enqueue(fn func()) {
	select {
	case s.cmds <- fn:
	default:
		// Buffer full. Spawn a goroutine to ensure delivery without
		// deadlocking the caller (who might be another Session).
		go func() { s.cmds <- fn }()
	}
}
