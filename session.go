package tether

import (
	"context"
	"maps"
	"sync/atomic"
	"time"

	"github.com/jpl-au/tether/wire"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/push"
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

// LiveSession represents a single connected client. Each browser tab
// gets its own LiveSession with independent state, a dedicated diff
// engine, and a command-loop goroutine that serialises all state
// mutations.
//
// All exported methods are safe to call from any goroutine — including
// from within Handle. The command loop processes them in order; there
// is no mutex and no deadlock risk.
type LiveSession[S any] struct {
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
	fxCh chan func(*Effects)

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

	// overflows counts how many times the command or effect buffer
	// was full and a goroutine was spawned to deliver the item.
	// Each overflow emits a BufferOverflow diagnostic.
	overflows atomic.Int64

	// overflowSem caps the number of concurrent overflow goroutines.
	// When both the command buffer and the semaphore are full, the
	// command is dropped and a CommandDropped diagnostic is emitted.
	overflowSem chan struct{}

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

	// userAgent is the User-Agent header captured when the session was
	// created. Used for session binding — on reconnect, the framework
	// verifies the reconnecting client's UA matches the original to
	// detect stolen session IDs.
	userAgent string

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

	// Component mounts for automatic event routing. Events matching
	// a mount's prefix are dispatched to the component before the
	// user's Handle function runs.
	mounts []ComponentMount[S]

	// Optional equality check — skip render when state unchanged.
	equal func(a, b S) bool

	// Optional telemetry hook for structural diff changes.
	onStructuralChange func(*LiveSession[S], StructuralChange)

	// Optional hook for render cycles that produce no patches.
	onNoPatch func(*LiveSession[S], NoPatch)

	// store is the external snapshot store from Config.DiffStore.
	// When non-nil, differ snapshots are saved here on disconnect
	// and deleted on reconnect or destroy, freeing process memory
	// during the reconnect window.
	store DiffStore

	// sessionStore is the external state store from
	// Config.SessionStore. When non-nil, session state S and
	// metadata are saved here on disconnect and graceful shutdown,
	// enabling crash recovery and node migration.
	sessionStore SessionStore

	// codec serialises state S for the session store. Set from
	// Config.Codec, or defaults to CBOR if nil.
	codec SessionCodec[S]

	// diagnostics is the handler's diagnostic bus. The session emits
	// transport errors, encode failures, panics, and buffer overflows.
	diagnostics *Bus[Diagnostic]
}

// Session is the interface every handler receives. It provides
// side-effect methods (Toast, Navigate, Signal, etc.) that work
// identically in live mode, stateless page mode, and tests.
//
// In live mode the underlying value is a [*LiveSession] which
// provides additional methods (Update, State, Close) via type
// assertion when needed. During pre-warming (initial GET) a
// capture implementation buffers side effects. In tethertest a
// test double captures them for assertions.
//
// Session is deliberately non-generic — component handlers can
// accept it without inheriting the application's state type
// parameter, making them reusable across different page states.
type Session interface {
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
	Push(n push.Notification) error
	// Close terminates the session by closing its transport. In
	// stateless page mode ([CaptureSession]) and tethertest this is
	// a no-op — there is no persistent connection to close.
	Close()
}

// CaptureSession implements [Session] by buffering side effects into
// an [Effects] struct instead of sending them to a client. It is used
// during pre-warming, stateless page handling, and testing.
//
// Create with a struct literal:
//
//	cs := &CaptureSession{SessionID: "my-id"}
//	// ... pass cs as Session ...
//	// ... read cs.Effects.Toast, cs.Effects.URL, etc.
//
// Compile-time interface satisfaction checks.
var (
	_ Session = (*CaptureSession)(nil)
	_ Session = (*LiveSession[struct{}])(nil)
	_ emitter = (*CaptureSession)(nil)
	_ emitter = (*LiveSession[struct{}])(nil)
)

type CaptureSession struct {
	// SessionID is returned by ID().
	SessionID string
	// PushErr is returned by Push(). Nil by default (appropriate for
	// tests); set to [ErrPushPreWarm] for pre-warming contexts.
	PushErr error
	// Effects holds the buffered side effects from the most recent
	// event cycle. Callers read these fields after Handle returns.
	Effects Effects
}

// ID returns the session identifier.
func (cs *CaptureSession) ID() string { return cs.SessionID }

// Context returns a detached background context. The session's real
// lifecycle context does not exist until the command loop starts, so
// pre-warm code cannot rely on cancellation propagation.
func (cs *CaptureSession) Context() context.Context { return context.Background() }

// Go spawns a goroutine against a background context. Anything
// launched during pre-warm runs independently — there is no command
// loop to synchronise with yet.
func (cs *CaptureSession) Go(fn func(context.Context)) {
	go fn(context.Background())
}

// enqueue executes fn synchronously. There is no command loop, so
// the function runs immediately in the caller's goroutine. This
// ensures Bus.Emit delivers events to subscribers during tests and
// pre-warming without requiring a live command loop.
func (cs *CaptureSession) enqueue(fn func()) { fn() }

// sessionID satisfies the emitter interface for Bus.Emit sender
// filtering. Subscribers whose sessionID matches the emitter's are
// skipped to prevent the sender from receiving its own broadcast.
func (cs *CaptureSession) sessionID() string { return cs.SessionID }

// Toast buffers a toast message into the effects struct. The message
// is delivered to the client in the first update after connection.
func (cs *CaptureSession) Toast(text string) { cs.Effects.Toast = text }

// Navigate buffers a client-side navigation. The redirect is applied
// in the first update after connection, before the client renders.
func (cs *CaptureSession) Navigate(rawURL string) {
	cs.Effects.URL = rawURL
	cs.Effects.Replace = false
}

// ReplaceURL buffers a history replacement. Unlike Navigate, this
// replaces the current URL without adding a history entry.
func (cs *CaptureSession) ReplaceURL(rawURL string) {
	cs.Effects.URL = rawURL
	cs.Effects.Replace = true
}

// SetTitle buffers a document title change for replay after connection.
func (cs *CaptureSession) SetTitle(title string) { cs.Effects.Title = title }

// Announce buffers an accessibility announcement (ARIA live region)
// for replay after connection.
func (cs *CaptureSession) Announce(text string) { cs.Effects.Announce = text }

// Push returns PushErr. Nil by default; set to [ErrPushPreWarm] for
// pre-warming contexts where no browser subscription exists yet.
func (cs *CaptureSession) Push(push.Notification) error {
	return cs.PushErr
}

// Close is a no-op. There is no transport to shut down and no
// command loop to stop.
func (cs *CaptureSession) Close() {}

// Flash buffers a targeted flash message keyed by CSS selector.
func (cs *CaptureSession) Flash(selector, text string) {
	if cs.Effects.Flash == nil {
		cs.Effects.Flash = make(map[string]string)
	}
	cs.Effects.Flash[selector] = text
}

// Signal buffers a single reactive value for the client.
func (cs *CaptureSession) Signal(key string, value any) {
	if cs.Effects.Signals == nil {
		cs.Effects.Signals = make(map[string]any)
	}
	cs.Effects.Signals[key] = value
}

// Signals buffers multiple reactive values for the client.
func (cs *CaptureSession) Signals(signals map[string]any) {
	if cs.Effects.Signals == nil {
		cs.Effects.Signals = make(map[string]any, len(signals))
	}
	maps.Copy(cs.Effects.Signals, signals)
}

// ID returns the unique session identifier. This is a cryptographically
// random string generated when the session is created. It can be used
// for logging, metrics, or as a key in external storage.
func (s *LiveSession[S]) ID() string {
	return s.id
}

// Context returns a context that is cancelled when the session is
// permanently destroyed (reaped or shutdown). The context survives
// temporary disconnects and reconnects — use it for background
// goroutines that should keep running while the client is away.
func (s *LiveSession[S]) Context() context.Context {
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
func (s *LiveSession[S]) Go(fn func(ctx context.Context)) {
	go fn(s.Context())
}

// sessionID returns the session's unique identifier. Used by
// Bus.Emit to record the sender for subscriber filtering.
func (s *LiveSession[S]) sessionID() string {
	return s.id
}

// enqueueFx sends an effect closure to the effects channel. Under
// normal load the send is non-blocking. When the buffer is full,
// an overflow goroutine delivers it — same semaphore-bounded
// pattern as [enqueue].
func (s *LiveSession[S]) enqueueFx(fn func(*Effects)) {
	select {
	case s.fxCh <- fn:
	default:
		s.logOverflow()
		select {
		case s.overflowSem <- struct{}{}:
			go func() {
				defer func() { <-s.overflowSem }()
				select {
				case s.fxCh <- fn:
				case <-s.ctx.Done():
				}
			}()
		default:
			s.emitDiagnostic(Diagnostic{
				Kind:      CommandDropped,
				SessionID: s.id,
				Detail:    s.endpoint,
			})
		}
	}
}

// logOverflow increments the overflow counter and emits a diagnostic.
func (s *LiveSession[S]) logOverflow() {
	s.overflows.Add(1)
	s.emitDiagnostic(Diagnostic{
		Kind:      BufferOverflow,
		SessionID: s.id,
		Detail:    s.endpoint,
	})
}

// emitDiagnostic publishes a diagnostic event to the handler's bus.
// No-op if the bus is nil (e.g. in tests that construct sessions
// directly without a handler).
func (s *LiveSession[S]) emitDiagnostic(d Diagnostic) {
	if s.diagnostics != nil {
		s.diagnostics.Publish(d)
	}
}

// drainFx collects all pending effects from fxCh into fx. Called
// on the loop goroutine after Handle/Update returns, before the
// render-diff-send pipeline. Pass nil to discard effects (e.g.
// after a panic).
func (s *LiveSession[S]) drainFx(fx *Effects) {
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
func (s *LiveSession[S]) sendFx(fx *Effects) {
	if !fx.Any() {
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
// The number of overflow goroutines is capped by a semaphore sized to
// CmdBufferSize. If both the buffer and the semaphore are full, the
// command is dropped and a [CommandDropped] diagnostic is emitted.
func (s *LiveSession[S]) enqueue(fn func()) {
	select {
	case s.cmds <- fn:
	default:
		s.logOverflow()
		select {
		case s.overflowSem <- struct{}{}:
			go func() {
				defer func() { <-s.overflowSem }()
				select {
				case s.cmds <- fn:
				case <-s.ctx.Done():
				}
			}()
		default:
			s.emitDiagnostic(Diagnostic{
				Kind:      CommandDropped,
				SessionID: s.id,
				Detail:    s.endpoint,
			})
		}
	}
}
