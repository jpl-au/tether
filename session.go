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
	handleParams func(PreSession, S, Params) S
	differ       *jit.Differ
	transport    Transport
	logger       *slog.Logger

	// Channel pair: events from transport, commands from everything else.
	events chan Event
	cmds   chan func()

	// Session lifetime — cancelled on permanent destruction.
	ctx  context.Context
	stop context.CancelFunc
	// closed by run() on exit so Shutdown can wait for the loop.
	loopDone chan struct{}

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

	// Last URL and title sent to the client. Captured in send() so
	// reattach can replay them — the browser's address bar and title
	// are separate from the DOM and would otherwise desync.
	lastURL   string
	lastTitle string

	// Push subscription — accessed only from within the loop.
	pushSub *PushSubscription

	// Buffered domain event publications, flushed after the client
	// update is sent. Populated by Bus.Emit via the emitter interface.
	emitted []func()

	// Installed by the Handler. Called when the transport reader
	// goroutine exits. Handles pool transitions
	// (active → disconnected or destroy).
	onDisconnect func()

	// Optional equality check — skip render when state unchanged.
	equal func(a, b S) bool

	// Optional telemetry hook for structural diff changes.
	onStructuralChange func(*Session[S], StructuralChange)
}

// PreSession is the subset of Session methods available in
// [Config.HandleParams]. During pre-warming (initial GET) no real
// session exists yet, so HandleParams receives a capture
// implementation that buffers side effects. During live navigation
// the real [Session] satisfies the interface.
type PreSession interface {
	ID() string
	Toast(text string)
	Navigate(rawURL string)
	ReplaceURL(rawURL string)
	SetTitle(title string)
	Announce(text string)
	Flash(selector, text string)
}

// captureSession implements PreSession by buffering side effects.
// Used during pre-warming to allow HandleParams to call SetTitle,
// Toast, etc. without panicking on a nil session.
type captureSession struct {
	id string
	fx *effects
}

func (c *captureSession) ID() string               { return c.id }
func (c *captureSession) Toast(text string)        { c.fx.toast = text }
func (c *captureSession) Navigate(rawURL string)   { c.fx.url = rawURL; c.fx.replace = false }
func (c *captureSession) ReplaceURL(rawURL string) { c.fx.url = rawURL; c.fx.replace = true }
func (c *captureSession) SetTitle(title string)    { c.fx.title = title }
func (c *captureSession) Announce(text string)     { c.fx.announce = text }

func (c *captureSession) Flash(selector, text string) {
	if c.fx.flash == nil {
		c.fx.flash = make(map[string]string)
	}
	c.fx.flash[selector] = text
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

// addEmission buffers a domain event publication. Only called when
// isHandling() is true — the loop goroutine is the only writer.
func (s *Session[S]) addEmission(fn func()) {
	s.emitted = append(s.emitted, fn)
}

// isHandling reports whether the session is inside exec() or an
// Update command. Used by Bus.Emit for the dual-path decision.
func (s *Session[S]) isHandling() bool {
	return s.handling
}

// sessionID returns the session's unique identifier. Used by
// Bus.Emit to record the sender for subscriber filtering.
func (s *Session[S]) sessionID() string {
	return s.id
}

// flushEmissions publishes all buffered domain events and clears
// the buffer. Called at the end of exec() and Update after the
// client update is sent.
func (s *Session[S]) flushEmissions() {
	for _, fn := range s.emitted {
		fn()
	}
	s.emitted = s.emitted[:0]
}

// enqueue pushes a command to the session's loop without blocking the
// caller. Under normal load the command goes straight into the buffered
// channel. When the buffer is full (e.g. during a Broadcast storm) a
// short-lived goroutine delivers the command instead, preventing
// cross-session deadlocks where two sessions broadcast to each other
// simultaneously with full buffers.
//
// The goroutine is context-aware: if the session is destroyed before
// the command can be delivered, the goroutine exits cleanly.
func (s *Session[S]) enqueue(fn func()) {
	select {
	case s.cmds <- fn:
	default:
		go func() {
			select {
			case s.cmds <- fn:
			case <-s.ctx.Done():
			}
		}()
	}
}
