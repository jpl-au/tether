package tether

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
)

// emitter is the internal capability marker that [Bus.Emit] uses to
// distinguish sessions with a live command loop from other [Session]
// implementations. It is unexported so developers never need to know it
// exists - they pass the Session they already have.
//
// [*StatefulSession] satisfies emitter via its command-loop enqueue.
// [*CaptureSession] satisfies emitter with synchronous enqueue  - 
// the function runs immediately in the caller's goroutine.
type emitter interface {
	enqueue(fn func())
	sessionID() string
}

// Bus routes typed domain events to subscribers. Create one per event
// type at program startup and share it across handlers:
//
//	var messages = tether.NewBus[MessageSent]()
//
// Bus enables cross-handler communication that [Group] cannot provide.
// Group requires all sessions to share the same state type. Bus is
// parameterised on the event type, so any session can subscribe
// regardless of its state.
//
// Internally the subscriber map is stored in an [atomic.Value] so
// publish is completely lock-free. Subscribe and unsubscribe use a
// write mutex and copy-on-write semantics - they are rare relative
// to publish so the copy cost is negligible.
type Bus[E any] struct {
	// wmu serialises writes (subscribe/unsubscribe). Reads go
	// through the atomic.Value and need no lock.
	wmu  sync.Mutex
	subs atomic.Value // holds map[uint64]subscriber[E]

	nextID uint64
}

type subscriber[E any] struct {
	fn        func(E)
	sessionID string          // empty for raw subscribers
	ctx       context.Context // auto-remove on cancellation
	async     bool            // true for SubscribeAsync subscribers
}

// NewBus creates an empty bus ready to accept subscribers.
func NewBus[E any]() *Bus[E] {
	b := &Bus[E]{}
	b.subs.Store(make(map[uint64]subscriber[E]))
	return b
}

// Emit publishes a domain event with sender filtering. Subscriptions
// registered via [On] whose session ID matches the emitting session
// are skipped - the sender's Handle already updated its own state.
//
// Behaviour varies by context:
//   - Stateful session ([*StatefulSession]): the publication is enqueued on the
//     session's command loop. This preserves ordering - the sender's
//     diff reaches the client before other subscribers react.
//   - Pre-warm or test ([*CaptureSession]): synchronous publish.
//     CaptureSession executes enqueue inline, so publish runs
//     immediately in the caller's goroutine - deterministic in tests,
//     harmless during pre-warm (no subscribers).
func (b *Bus[E]) Emit(s Session, event E) {
	if em, ok := s.(emitter); ok {
		sid := em.sessionID()
		em.enqueue(func() { b.publish(event, sid) })
		return
	}
	// Partial session without emitter: synchronous publish.
	b.publish(event, s.ID())
}

// Publish sends an event to all subscribers with no sender filter.
// Use this for external event sources (database change listeners,
// message queue consumers, cron jobs) that have no session identity.
//
// Subscriber callbacks run synchronously in the caller's goroutine.
// Session-bound subscribers (registered via [On]) are non-blocking
// because they route through the session's command channel, but raw
// [Subscribe] callbacks that block will stall the caller.
func (b *Bus[E]) Publish(event E) {
	b.publish(event, "")
}

// Subscribe registers a callback that receives every event (no sender
// filter). The subscription lives until ctx is cancelled. Returns an
// unsubscribe function for early removal.
//
// The callback runs synchronously in the publisher's goroutine - it
// must not block. If the publisher is a session (via [Bus.Emit]), a
// blocking callback stalls that session's command loop. For expensive
// work, use [Bus.SubscribeAsync] or [On] which routes through the
// subscriber's own command loop.
func (b *Bus[E]) Subscribe(ctx context.Context, fn func(E)) func() {
	return b.subscribe(ctx, fn, "")
}

// SubscribeAsync registers a callback that receives every event in its
// own goroutine. Each publication spawns a new goroutine for the
// callback, so the publisher never blocks regardless of how long the
// callback takes.
//
// Use this for external consumers that perform I/O (database writes,
// HTTP calls, logging) in response to events. For session-bound
// subscriptions, prefer [On] which routes through the session's
// command loop. The subscription lives until ctx is cancelled.
// Returns an unsubscribe function for early removal.
func (b *Bus[E]) SubscribeAsync(ctx context.Context, fn func(E)) func() {
	return b.subscribeAsync(ctx, fn, "")
}

// Len returns the number of active subscribers. Lock-free.
func (b *Bus[E]) Len() int {
	return len(b.loadSubs())
}

// subscribe is the internal registration method. sessionID is non-empty
// for subscriptions created via tether.On (sender-filtered) and empty for
// raw Subscribe calls. Copy-on-write: a new map is built under the
// write lock and stored atomically.
func (b *Bus[E]) subscribe(ctx context.Context, fn func(E), sessionID string) func() {
	return b.add(ctx, fn, sessionID, false)
}

// subscribeAsync is like subscribe but marks the subscriber as async.
// The callback runs in its own goroutine on each publication.
func (b *Bus[E]) subscribeAsync(ctx context.Context, fn func(E), sessionID string) func() {
	return b.add(ctx, fn, sessionID, true)
}

// add is the core registration method. All subscription variants
// (sync, async, session-bound, raw) funnel through here. Copy-on-write:
// a new map is built under the write lock and stored atomically.
func (b *Bus[E]) add(ctx context.Context, fn func(E), sessionID string, async bool) func() {
	b.wmu.Lock()
	id := b.nextID
	b.nextID++

	old := b.loadSubs()
	subs := make(map[uint64]subscriber[E], len(old)+1)
	maps.Copy(subs, old)
	subs[id] = subscriber[E]{
		fn:        fn,
		sessionID: sessionID,
		ctx:       ctx,
		async:     async,
	}
	b.subs.Store(subs)
	b.wmu.Unlock()

	unsub := func() { b.remove(id) }
	context.AfterFunc(ctx, unsub)
	return unsub
}

// remove deletes a subscriber by ID via copy-on-write. Called by the
// unsubscribe function and by context.AfterFunc on cancellation.
func (b *Bus[E]) remove(id uint64) {
	b.wmu.Lock()
	defer b.wmu.Unlock()

	old := b.loadSubs()
	if _, ok := old[id]; !ok {
		return // already removed (double-cancel or explicit unsub + ctx cancel)
	}
	subs := make(map[uint64]subscriber[E], len(old))
	for k, v := range old {
		if k != id {
			subs[k] = v
		}
	}
	b.subs.Store(subs)
}

// publish iterates the subscriber map with no lock - the atomic load
// returns a consistent snapshot. Subscribers whose sessionID matches
// senderID are skipped. Async subscribers run in their own goroutine.
func (b *Bus[E]) publish(event E, senderID string) {
	for _, s := range b.loadSubs() {
		if s.ctx.Err() != nil {
			continue // dead subscriber, skip
		}
		if senderID != "" && s.sessionID == senderID {
			continue // sender filtering
		}
		if s.async {
			go s.fn(event)
		} else {
			s.fn(event)
		}
	}
}

// loadSubs returns the current subscriber map from the atomic.Value.
// Returns an empty map if the Bus was not created via [NewBus].
func (b *Bus[E]) loadSubs() map[uint64]subscriber[E] {
	v := b.subs.Load()
	if v == nil {
		return nil
	}
	return v.(map[uint64]subscriber[E])
}
