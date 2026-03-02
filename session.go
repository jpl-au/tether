package tether

import (
	"context"
	"maps"
	"sync/atomic"
	"time"

	"github.com/jpl-au/fluent-tether/dev"
	"github.com/jpl-au/fluent-tether/wire"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent-tether/push"
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

// NoPatch describes a render cycle that produced no DOM changes.
// Passed to [Config.OnNoPatch] so the developer can log, count, or
// ignore it as appropriate.
type NoPatch struct {
	Source string // "update", "navigate", or "event"
	Action string // event action; empty for "update" source
}

// RenderFunc builds a Fluent node tree from the current state. It is
// called on initial page render, after each client event, and after
// each call to [Session.Update]. The function must be pure — given the
// same state it must always produce the same tree, because the diff
// engine compares consecutive renders to compute patches.
type RenderFunc[S any] func(state S) node.Node

// defaultCmdBufferSize is the capacity of the command channel when
// [Config].CmdBufferSize is zero.
const defaultCmdBufferSize = 64

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

	render    RenderFunc[S]
	handle    HandleFunc[S]
	differ    *jit.Differ
	encoder   wire.Encoder
	transport Transport

	// Channel pair: events from transport, commands from everything else.
	events chan Event
	cmds   chan func()
	// fxCh receives effect closures (Toast, Signal, etc.) from any
	// goroutine. The loop drains it after Handle/Update returns so
	// effects are sent atomically with the diff. Outside of Handle,
	// the loop picks them up and sends them as standalone updates.
	fxCh chan func(*effects)

	// Session lifetime — cancelled on permanent destruction.
	ctx  context.Context
	stop context.CancelFunc
	// closed by run() on exit so Shutdown can wait for the loop.
	loopDone chan struct{}

	// Timestamps. lastActivity is atomic so the idle timer reset
	// (inside the loop) and external readers (Health) don't conflict.
	lastActivity atomic.Int64 // UnixNano
	createdAt    time.Time

	// loopRunning is set to true when run() enters its select loop.
	// State() checks this to avoid deadlocking when called before
	// the loop has started (e.g. during OnConnect).
	loopRunning atomic.Bool
	// handling is true while the loop goroutine is inside exec() or
	// Update. Used by State() to choose the fast path (return the
	// atomic snapshot) instead of routing through the command channel,
	// which would deadlock because the loop is busy in Handle.
	handling atomic.Bool
	// stateSnap holds the state value captured at the start of each
	// exec/Update cycle. It is stored atomically before handling is
	// set to true, so any goroutine that sees handling=true can safely
	// read the snapshot without a data race on s.state.
	stateSnap atomic.Value

	// Lifecycle timers (replace centralised reaper).
	idleTimer   *time.Timer
	idleTimeout time.Duration
	// disconnectTimer is started when the transport closes and
	// stopped on reattach. If it fires, the session is destroyed.
	disconnectTimer  *time.Timer
	reconnectTimeout time.Duration

	// endpoint is the URL path the session was created on (from the
	// initial GET or direct transport connect). Used in log messages
	// so errors can be traced back to a page.
	endpoint string

	// Last URL and title sent to the client. Captured in send() so
	// reattach can replay them — the browser's address bar and title
	// are separate from the DOM and would otherwise desync.
	lastURL   string
	lastTitle string

	// Push — sender is set from Config, subscription arrives at runtime.
	// pushSub is atomic so Push() can read it safely from any goroutine
	// without routing through the command channel.
	pushSender *push.Sender
	pushSub    atomic.Pointer[push.Subscription]

	// Installed by the Handler. Called when the transport reader
	// goroutine exits. Handles pool transitions
	// (active → disconnected or destroy).
	onDisconnect func()

	// Optional equality check — skip render when state unchanged.
	equal func(a, b S) bool

	// Optional telemetry hook for structural diff changes.
	onStructuralChange func(*Session[S], StructuralChange)

	// Optional hook for render cycles that produce no patches.
	onNoPatch func(*Session[S], NoPatch)
}

// PreSession is the subset of Session methods available in
// [Config.OnNavigate] and reusable components. During pre-warming
// (initial GET) no real session exists yet, so OnNavigate receives
// a capture implementation that buffers side effects. During live
// navigation the real [Session] satisfies the interface.
//
// PreSession is deliberately non-generic — component handlers can
// accept it without inheriting the application's state type
// parameter, making them reusable across different page states.
type PreSession interface {
	// ID returns the session identifier. In live mode this is the
	// unique, cryptographically random session ID. In stateless page
	// mode (PageConfig) this returns an empty string because there is
	// no persistent session. In tethertest it returns "tethertest".
	ID() string
	Context() context.Context
	Go(fn func(context.Context))
	Toast(text string)
	Navigate(rawURL string)
	ReplaceURL(rawURL string)
	SetTitle(title string)
	Announce(text string)
	Flash(selector, text string)
	Signal(key string, value any)
	Signals(signals map[string]any)
	SignalBatch(pairs ...any)
	Push(n push.Notification) error
}

// captureSession implements PreSession by buffering side effects.
// Used during pre-warming to allow OnNavigate to call SetTitle,
// Toast, etc. without panicking on a nil session.
type captureSession struct {
	id string
	fx *effects
}

func (c *captureSession) ID() string               { return c.id }
func (c *captureSession) Context() context.Context { return context.Background() }
func (c *captureSession) Go(fn func(context.Context)) {
	go fn(context.Background())
}
func (c *captureSession) Toast(text string)        { c.fx.toast = text }
func (c *captureSession) Navigate(rawURL string)   { c.fx.url = rawURL; c.fx.replace = false }
func (c *captureSession) ReplaceURL(rawURL string) { c.fx.url = rawURL; c.fx.replace = true }
func (c *captureSession) SetTitle(title string)    { c.fx.title = title }
func (c *captureSession) Announce(text string)     { c.fx.announce = text }

// Push returns an error during pre-warming because no browser
// subscription exists yet.
func (c *captureSession) Push(push.Notification) error {
	return ErrPushPreWarm
}

func (c *captureSession) Flash(selector, text string) {
	if c.fx.flash == nil {
		c.fx.flash = make(map[string]string)
	}
	c.fx.flash[selector] = text
}

func (c *captureSession) Signal(key string, value any) {
	if c.fx.signals == nil {
		c.fx.signals = make(map[string]any)
	}
	c.fx.signals[key] = value
}

func (c *captureSession) Signals(signals map[string]any) {
	if c.fx.signals == nil {
		c.fx.signals = make(map[string]any, len(signals))
	}
	maps.Copy(c.fx.signals, signals)
}

func (c *captureSession) SignalBatch(pairs ...any) {
	c.Signals(pairsToMap(pairs))
}

// pairsToMap converts a flat key-value list ("k1", v1, "k2", v2, ...)
// into a map. Panics if the count is odd or any key is not a string.
func pairsToMap(pairs []any) map[string]any {
	if len(pairs)%2 != 0 {
		panic("tether: SignalBatch requires an even number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			panic("tether: SignalBatch keys must be strings")
		}
		m[key] = pairs[i+1]
	}
	return m
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

// sessionID returns the session's unique identifier. Used by
// Bus.Emit to record the sender for subscriber filtering.
func (s *Session[S]) sessionID() string {
	return s.id
}

// enqueueFx sends an effect closure to the effects channel. Under
// normal load the send is non-blocking. When the buffer is full,
// an overflow goroutine delivers it — same pattern as [enqueue].
func (s *Session[S]) enqueueFx(fn func(*effects)) {
	select {
	case s.fxCh <- fn:
	default:
		go func() {
			select {
			case s.fxCh <- fn:
			case <-s.ctx.Done():
			}
		}()
	}
}

// drainFx collects all pending effects from fxCh into fx. Called
// on the loop goroutine after Handle/Update returns, before the
// render-diff-send pipeline. Pass nil to discard effects (e.g.
// after a panic).
func (s *Session[S]) drainFx(fx *effects) {
	for {
		select {
		case fn := <-s.fxCh:
			if fx != nil {
				fn(fx)
			}
		default:
			return
		}
	}
}

// sendFx sends any accumulated effects as a standalone update.
// Used by the loop when effects arrive outside of Handle.
func (s *Session[S]) sendFx(fx *effects) {
	if !fx.any() {
		return
	}
	var u wire.Update
	fx.merge(&u)
	s.send(u)
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
		// Command buffer full — overflow to a goroutine to prevent
		// deadlock. This is expected during broadcast storms but
		// sustained overflow suggests the buffer is too small.
		dev.Debug("command buffer full, overflow to goroutine", "session", s.id, "endpoint", s.endpoint, "url", s.lastURL)
		go func() {
			select {
			case s.cmds <- fn:
			case <-s.ctx.Done():
			}
		}()
	}
}
