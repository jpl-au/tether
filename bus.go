package poly

import (
	"context"
	"sync"
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
type Bus[E any] struct {
	mu     sync.RWMutex
	subs   map[uint64]subscriber[E]
	nextID uint64
}

type subscriber[E any] struct {
	fn        func(E)
	sessionID string          // empty for raw subscribers
	ctx       context.Context // auto-remove on cancellation
}

// NewBus creates an empty bus ready to accept subscribers.
func NewBus[E any]() *Bus[E] {
	return &Bus[E]{
		subs: make(map[uint64]subscriber[E]),
	}
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

// Len returns the number of active subscribers.
func (b *Bus[E]) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// subscribe is the internal registration method. sessionID is non-empty
// for subscriptions created via poly.On (sender-filtered) and empty for
// raw Subscribe calls.
func (b *Bus[E]) subscribe(ctx context.Context, fn func(E), sessionID string) func() {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = subscriber[E]{
		fn:        fn,
		sessionID: sessionID,
		ctx:       ctx,
	}
	b.mu.Unlock()

	unsub := func() { b.remove(id) }
	context.AfterFunc(ctx, unsub)
	return unsub
}

// remove deletes a subscriber by ID. O(1) via map lookup. Called by
// the unsubscribe function and by context.AfterFunc on cancellation.
func (b *Bus[E]) remove(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, id)
}

// publish snapshots subscribers under a read lock, then invokes
// callbacks without the lock — same pattern as Group.Broadcast.
// Subscribers whose sessionID matches senderID are skipped.
func (b *Bus[E]) publish(event E, senderID string) {
	b.mu.RLock()
	// Snapshot to avoid holding the lock during callbacks.
	targets := make([]subscriber[E], 0, len(b.subs))
	for _, s := range b.subs {
		targets = append(targets, s)
	}
	b.mu.RUnlock()

	for _, s := range targets {
		if s.ctx.Err() != nil {
			continue // dead subscriber, skip
		}
		if senderID != "" && s.sessionID == senderID {
			continue // sender filtering
		}
		s.fn(event)
	}
}
