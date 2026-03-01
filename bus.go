package poly

import (
	"context"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
)

// Emitter identifies the session that published a domain event. It is
// the bridge between [Bus] and [Session] — Bus.Emit needs to enqueue
// a publication without knowing the session's state type parameter
// S. [*Session] is the only type that implements Emitter; the methods
// are unexported to prevent external implementations.
type Emitter interface {
	enqueue(fn func())
	sessionID() string
}

// Bus routes typed domain events to subscribers. Create one per event
// type at program startup and share it across handlers:
//
//	var messages = poly.NewBus[MessageSent]()
//
// Bus enables cross-handler communication that [Group] cannot provide.
// Group requires all sessions to share the same state type. Bus is
// parameterised on the event type, so any session can subscribe
// regardless of its state.
//
// Internally the subscriber map is stored in an [atomic.Value] so
// publish is completely lock-free. Subscribe and unsubscribe use a
// write mutex and copy-on-write semantics — they are rare relative
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
}

// NewBus creates an empty bus ready to accept subscribers.
func NewBus[E any]() *Bus[E] {
	b := &Bus[E]{}
	b.subs.Store(make(map[uint64]subscriber[E]))
	return b
}

// Emit publishes a domain event with sender filtering. Subscriptions
// registered via [On] whose session ID matches the emitting session
// are skipped — the sender's Handle already updated its own state.
//
// The publication is always enqueued as a command on the emitting
// session's loop. This ensures the event is published after the
// current exec/Update cycle completes — the sender's diff is sent
// to the client before other subscribers react.
func (b *Bus[E]) Emit(s Emitter, event E) {
	sid := s.sessionID()
	s.enqueue(func() { b.publish(event, sid) })
}

// Publish sends an event to all subscribers with no sender filter.
// Use this for external event sources (database change listeners,
// message queue consumers, cron jobs) that have no session identity.
func (b *Bus[E]) Publish(event E) {
	b.publish(event, "")
}

// Subscribe registers a callback that receives every event (no sender
// filter). The subscription lives until ctx is cancelled. Returns an
// unsubscribe function for early removal.
func (b *Bus[E]) Subscribe(ctx context.Context, fn func(E)) func() {
	return b.subscribe(ctx, fn, "")
}

// Len returns the number of active subscribers. Lock-free.
func (b *Bus[E]) Len() int {
	return len(b.loadSubs())
}

// subscribe is the internal registration method. sessionID is non-empty
// for subscriptions created via poly.On (sender-filtered) and empty for
// raw Subscribe calls. Copy-on-write: a new map is built under the
// write lock and stored atomically.
func (b *Bus[E]) subscribe(ctx context.Context, fn func(E), sessionID string) func() {
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
	}
	b.subs.Store(subs)
	b.wmu.Unlock()

	slog.Debug("bus.subscribe", "session", sessionID, "subscribers", len(subs))

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

// publish iterates the subscriber map with no lock — the atomic load
// returns a consistent snapshot. Subscribers whose sessionID matches
// senderID are skipped.
func (b *Bus[E]) publish(event E, senderID string) {
	slog.Debug("bus.publish", "sender", senderID, "subscribers", b.Len())
	for _, s := range b.loadSubs() {
		if s.ctx.Err() != nil {
			continue // dead subscriber, skip
		}
		if senderID != "" && s.sessionID == senderID {
			continue // sender filtering
		}
		s.fn(event)
	}
}

// loadSubs returns the current subscriber map from the atomic.Value.
func (b *Bus[E]) loadSubs() map[uint64]subscriber[E] {
	return b.subs.Load().(map[uint64]subscriber[E])
}
