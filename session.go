package tether

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jpl-au/tether/wire"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/push"
)

// StructuralChange describes a diff result where the render tree's
// Dynamic key set changed - keys were added, removed, or reordered.
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
// Passed to [StatefulConfig.OnNoPatch] so the developer can log, count, or
// ignore it as appropriate.
type NoPatch struct {
	Source string // "update", "navigate", or "event"
	Action string // event action; empty for "update" source
}

// RenderFunc builds a Fluent node tree from the current state. It is
// called on initial page render, after each client event, and after
// each call to [Session.Update]. The function must be pure - given the
// same state it must always produce the same tree, because the diff
// engine compares consecutive renders to compute patches.
type RenderFunc[S any] func(state S) node.Node

// defaultCmdBufferSize is the capacity of the command channel when
// [StatefulConfig].CmdBufferSize is zero.
const defaultCmdBufferSize = 64

// StatefulSession represents a single connected client. Each browser tab
// gets its own StatefulSession with independent state, a dedicated diff
// engine, and a command-loop goroutine that serialises all state
// mutations.
//
// All exported methods are safe to call from any goroutine - including
// from within Handle. The command loop processes them in order; there
// is no mutex and no deadlock risk.
type StatefulSession[S any] struct {
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

	// Session lifetime - cancelled on permanent destruction.
	ctx  context.Context
	stop context.CancelFunc
	// Transport lifetime - cancelled when the transport drops
	// (disconnect or freeze). Recreated on reattach and thaw.
	// Go() passes this context so background goroutines stop
	// when the client is no longer connected.
	transportCtx    context.Context
	transportCancel context.CancelFunc
	// loopDone is closed each time run() exits. The HTTP handler
	// goroutine blocks on this so it can return when the transport
	// is no longer needed. A frozen session closes loopDone on
	// freeze and creates a new one on thaw.
	loopDone chan struct{}
	// destroyed is closed when the session reaches the Destroyed
	// state. Shutdown and Drain block on this to wait for permanent
	// cleanup - not just a loop exit (which also happens on freeze).
	// Multiple code paths may attempt to close it (cleanup and
	// destroySession); destroyedOnce ensures exactly one close.
	destroyed     chan struct{}
	destroyedOnce sync.Once

	// Timestamps. lastActivity is atomic so the idle timer reset
	// (inside the loop) and external readers (Health) don't conflict.
	lastActivity atomic.Int64 // UnixNano
	createdAt    time.Time

	// status tracks the session's lifecycle state. Transitions are
	// Pending → Active → Frozen → Active (thaw) or → Destroyed.
	// See [Status] for the full state machine.
	status atomic.Int32
	// stateSnap holds the most recently completed state, updated
	// atomically after every mutation in exec() and Update(). State()
	// returns this value when the loop is active - no channel
	// round-trip, no blocking.
	stateSnap atomic.Value

	// handling is true while Handle or Update is executing on the
	// loop goroutine. Used by State() to emit a dev-mode warning
	// when called during Handle - the snapshot is stale and the
	// developer should use the state parameter instead.
	handling bool

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
	// lifetimeTimer caps the session's total duration. Stopped in
	// cleanup to avoid a leaked goroutine when the session is
	// destroyed before MaxLifetime fires.
	lifetimeTimer *time.Timer

	// endpoint is the URL path the session was created on (from the
	// initial GET or direct transport connect). Used in log messages
	// so errors can be traced back to a page.
	endpoint string

	// userAgent is the User-Agent header captured when the session was
	// created. Used for session binding - on reconnect, the framework
	// verifies the reconnecting client's UA matches the original to
	// detect stolen session IDs.
	userAgent string

	// Last URL and title sent to the client. Captured in send() so
	// reattach can replay them - the browser's address bar and title
	// are separate from the DOM and would otherwise desync.
	lastURL   string
	lastTitle string

	// Push - sender is set from StatefulConfig, subscription arrives at runtime.
	// pushSub is atomic so Push() can read it safely from any goroutine
	// without routing through the command channel.
	pushSender *push.Sender
	pushSub    atomic.Pointer[push.Subscription]

	// handler is the owning Handler. Used by onTransportClose and
	// the disconnect timer to perform pool transitions without
	// mutable callback fields. Set once during session creation
	// and never changed.
	handler *Handler[S]

	// Optional equality check - skip render when state unchanged.
	equal func(a, b S) bool

	// Optional telemetry hook for structural diff changes.
	onStructuralChange func(*StatefulSession[S], StructuralChange)

	// Optional hook for render cycles that produce no patches.
	onNoPatch func(*StatefulSession[S], NoPatch)

	// store is the external snapshot store from StatefulConfig.DiffStore.
	// When non-nil, differ snapshots are saved here on disconnect
	// and deleted on reconnect or destroy, freeing process memory
	// during the reconnect window.
	store DiffStore

	// sessionStore is the external state store from
	// StatefulConfig.SessionStore. When non-nil, session state S and
	// metadata are saved here on disconnect and graceful shutdown,
	// enabling crash recovery and node migration.
	sessionStore SessionStore

	// codec serialises state S for the session store. Set from
	// StatefulConfig.Codec, or defaults to CBOR if nil.
	codec SessionCodec[S]

	// onPanic is called when a panic occurs during Handle or Update.
	// When nil, the session is destroyed. When set, the session
	// survives and the developer assumes responsibility for state
	// integrity. Set from StatefulConfig.OnPanic.
	onPanic func(*StatefulSession[S], error)

	// onCommandDropped is called when a command is dropped because
	// the buffer and overflow semaphore are both full. When nil,
	// the session is destroyed. Set from StatefulConfig.OnCommandDropped.
	onCommandDropped func(*StatefulSession[S])

	// freeze is true when Freeze is enabled and a SessionStore is
	// configured. When set, the session persists state to the store
	// on disconnect, releases S and the differ, and exits the
	// command loop - reducing memory to metadata only.
	freeze bool

	// diagnostics is the handler's diagnostic bus. The session emits
	// transport errors, encode failures, panics, and buffer overflows.
	diagnostics *Bus[Diagnostic]
}

// Session is the interface every handler receives. It provides
// side-effect methods (Toast, Navigate, Signal, etc.) that work
// identically in stateful mode, stateless mode, and tests.
//
// In stateful mode the underlying value is a [*StatefulSession] which
// provides additional methods (Update, State, Close) via type
// assertion when needed. During pre-warming (initial GET) a
// capture implementation buffers side effects. In tethertest a
// test double captures them for assertions.
//
// Session is deliberately non-generic - component handlers can
// accept it without inheriting the application's state type
// parameter, making them reusable across different page states.
type Session interface {
	// ID returns the session identifier. In stateful mode this is the
	// unique, cryptographically random session ID. In stateless
	// mode (StatelessConfig) this returns an empty string because there is
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
	ScrollTo(selector string)
	Signal(key string, value any)
	Signals(signals map[string]any)
	Push(n push.Notification) error
	// Close terminates the session by closing its transport. In
	// stateless mode ([CaptureSession]) and tethertest this is
	// a no-op - there is no persistent connection to close.
	Close()
}

// CaptureSession implements [Session] by buffering side effects into
// an [Effects] struct instead of sending them to a client. It is used
// during pre-warming, stateless page handling, and testing.
//
// Create with a struct literal. In production, set Ctx to the HTTP
// request context so goroutines spawned via [Session.Go] are cancelled
// when the client disconnects:
//
//	cs := &CaptureSession{Ctx: r.Context(), PushErr: ErrPushPreWarm}
//
// In tests, Ctx can be omitted (defaults to context.Background):
//
//	cs := &CaptureSession{SessionID: "my-id"}
//
// Compile-time interface satisfaction checks.
var (
	_ Session = (*CaptureSession)(nil)
	_ Session = (*StatefulSession[struct{}])(nil)
	_ emitter = (*CaptureSession)(nil)
	_ emitter = (*StatefulSession[struct{}])(nil)
)

type CaptureSession struct {
	// SessionID is returned by ID().
	SessionID string
	// Ctx is the context returned by Context() and passed to Go().
	// When nil, context.Background() is used. Set this to the HTTP
	// request context during pre-warming and stateless handling so
	// goroutines spawned via Go() are cancelled when the client
	// disconnects.
	Ctx context.Context
	// PushErr is returned by Push(). Nil by default (appropriate for
	// tests); set to [ErrPushPreWarm] for pre-warming contexts.
	PushErr error
	// Effects holds the buffered side effects from the most recent
	// event cycle. Callers read these fields after Handle returns.
	Effects Effects
}

// ID returns the session identifier.
func (cs *CaptureSession) ID() string { return cs.SessionID }

// Context returns the context set via the Ctx field. When Ctx is nil
// (the default in tests), context.Background() is returned. In
// production, Ctx is the HTTP request context so goroutines are
// cancelled when the client disconnects.
func (cs *CaptureSession) Context() context.Context {
	if cs.Ctx != nil {
		return cs.Ctx
	}
	return context.Background()
}

// Go spawns a goroutine bound to the session's context. When Ctx is
// set to the HTTP request context, the goroutine is cancelled if the
// client disconnects before it finishes.
func (cs *CaptureSession) Go(fn func(context.Context)) {
	go fn(cs.Context())
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

// ScrollTo buffers a scroll-into-view command for replay after connection.
func (cs *CaptureSession) ScrollTo(selector string) { cs.Effects.ScrollTo = selector }

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
func (s *StatefulSession[S]) ID() string {
	return s.id
}

// Context returns a context that is cancelled when the session is
// permanently destroyed (reaped or shutdown). The context survives
// temporary disconnects and reconnects - use it for background
// goroutines that should keep running while the client is away.
func (s *StatefulSession[S]) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// Go launches a goroutine bound to the transport's lifetime. The
// context passed to fn is cancelled when the client disconnects or
// the session freezes. Use this in OnConnect for background work
// like tickers, watchers, or change listeners that should stop
// when the client is no longer connected.
//
// On reconnect or thaw, OnConnect/OnRestore fires again and can
// spawn fresh goroutines. This prevents duplicate goroutines from
// accumulating across disconnect/reconnect cycles.
//
// For goroutines that must survive disconnects (rare), use
// [StatefulSession.Context] directly: go fn(sess.Context()).
//
// The goroutine must respect context cancellation. A goroutine
// that ignores the context will leak.
func (s *StatefulSession[S]) Go(fn func(ctx context.Context)) {
	go fn(s.transportCtx)
}

// sessionID returns the session's unique identifier. Used by
// Bus.Emit to record the sender for subscriber filtering.
func (s *StatefulSession[S]) sessionID() string {
	return s.id
}

// enqueueFx sends an effect closure to the effects channel. Under
// normal load the send is non-blocking. When the buffer is full,
// an overflow goroutine delivers it - same semaphore-bounded
// pattern as [enqueue].
func (s *StatefulSession[S]) enqueueFx(fn func(*Effects)) {
	st := Status(s.status.Load())
	if st == Frozen || st == Destroyed {
		s.emitDiagnostic(Diagnostic{
			Kind:      CommandDiscarded,
			SessionID: s.id,
			Detail:    st.String(),
		})
		return
	}
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
				case <-s.loopDone:
				}
			}()
		default:
			s.commandDropped()
		}
	}
}

// commandDropped handles the case where both the command buffer and
// overflow semaphore are full. By default the session is destroyed
// to prevent silent client drift. If OnCommandDropped is set, the
// developer handles it instead.
func (s *StatefulSession[S]) commandDropped() {
	s.emitDiagnostic(Diagnostic{
		Kind:      CommandDropped,
		SessionID: s.id,
		Detail:    s.endpoint,
	})
	if s.onCommandDropped != nil {
		s.onCommandDropped(s)
	} else {
		s.stop()
	}
}

// logOverflow increments the overflow counter and emits a diagnostic.
func (s *StatefulSession[S]) logOverflow() {
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
func (s *StatefulSession[S]) emitDiagnostic(d Diagnostic) {
	if s.diagnostics != nil {
		s.diagnostics.Publish(d)
	}
}

// drainFx collects all pending effects from fxCh into fx. Called
// on the loop goroutine after Handle/Update returns, before the
// render-diff-send pipeline. Pass nil to discard effects (e.g.
// after a panic).
func (s *StatefulSession[S]) drainFx(fx *Effects) {
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
func (s *StatefulSession[S]) sendFx(fx *Effects) {
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
// command is dropped and the session is destroyed unless the developer
// has set [StatefulConfig.OnCommandDropped].
func (s *StatefulSession[S]) enqueue(fn func()) {
	st := Status(s.status.Load())
	if st == Frozen || st == Destroyed {
		s.emitDiagnostic(Diagnostic{
			Kind:      CommandDiscarded,
			SessionID: s.id,
			Detail:    st.String(),
		})
		return
	}
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
				case <-s.loopDone:
				}
			}()
		default:
			s.commandDropped()
		}
	}
}
