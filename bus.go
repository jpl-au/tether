package tether

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/jpl-au/tether/dev"
)

// defaultAsyncWorkers is the semaphore size for async subscribers
// when [BusConfig].AsyncWorkers is zero.
const defaultAsyncWorkers = 64

// AsyncOverflow controls what happens when all async worker slots
// are occupied during publication.
type AsyncOverflow int

const (
	// Block waits for a semaphore slot before spawning the
	// goroutine. No data loss, but the publisher's goroutine
	// stalls until a worker finishes. This is the default.
	Block AsyncOverflow = iota + 1

	// Drop discards the event for this subscriber and logs a
	// warning. The publisher never stalls.
	Drop

	// Inline runs the callback synchronously in the publisher's
	// goroutine. No data loss, but the publisher blocks for the
	// full duration of the callback.
	Inline
)

// BusConfig customises the behaviour of a [Bus]. Pass to [NewBus]
// to override defaults.
type BusConfig struct {
	// Topic enables cluster distribution for this bus. When set and
	// a [Cluster] is configured on [App], events are published to
	// the cluster topic and remote events are delivered to local
	// subscribers. Leave empty for local-only operation.
	Topic string

	// AsyncWorkers limits the number of concurrent goroutines for
	// async subscribers. Each publication acquires a semaphore slot
	// before spawning a goroutine. Default 64.
	AsyncWorkers int

	// AsyncOverflow controls what happens when all worker slots
	// are occupied. Default [Block].
	AsyncOverflow AsyncOverflow
}

// emitter is the internal capability marker that [Bus.Emit] uses to
// distinguish sessions with a live command loop from other [Session]
// implementations. It is unexported so developers never need to know it
// exists - they pass the Session they already have.
//
// [*StatefulSession] satisfies emitter via its command-loop enqueue.
// [*CaptureSession] satisfies emitter with synchronous enqueue -
// the function runs immediately in the caller's goroutine.
type emitter interface {
	enqueue(fn func())
	sessionID() string
}

// Bus routes typed domain events to subscribers. Create one per event
// type at program startup and share it across handlers:
//
//	var messages = tether.NewBus[MessageSent](tether.BusConfig{Topic: "messages"})
//
// Bus enables cross-handler communication that [Group] cannot provide.
// Group requires all sessions to share the same state type. Bus is
// parameterised on the event type, so any session can subscribe
// regardless of its state.
//
// When created with a [BusConfig] containing a Topic, the Bus
// publishes events to the cluster (if configured on [App]) and
// receives events from other nodes. Without a Topic, the Bus is
// local-only.
//
// Internally the subscriber map is stored in an [atomic.Value] so
// publish is completely lock-free. Subscribe and unsubscribe use a
// write mutex and copy-on-write semantics - they are rare relative
// to publish so the copy cost is negligible.
//
// Async subscribers are bounded by a semaphore (default 64 workers).
// When all slots are occupied, the overflow strategy controls
// behaviour: [Block] (default), [Drop], or [Inline]. Configure via
// [BusConfig] in [NewBus].
type Bus[E any] struct {
	// wmu serialises writes (subscribe/unsubscribe). Reads go
	// through the atomic.Value and need no lock.
	wmu  sync.Mutex
	subs atomic.Value // holds map[uint64]subscriber[E]

	nextID   uint64
	sem      chan struct{} // bounds concurrent async goroutines
	overflow AsyncOverflow

	// topic is the cluster topic name. Empty means local-only.
	topic string

	// clusterOnce ensures we subscribe to the cluster topic at most
	// once, lazily on first publish or explicit subscribe.
	clusterOnce  sync.Once
	clusterUnsub func()
}

type subscriber[E any] struct {
	fn        func(E)
	sessionID string          // empty for raw subscribers
	ctx       context.Context // auto-remove on cancellation
	async     bool            // true for SubscribeAsync subscribers
}

// NewBus creates an empty bus ready to accept subscribers. An optional
// [BusConfig] customises async worker limits, overflow behaviour, and
// the cluster topic. Without config, the bus operates locally with 64
// concurrent async workers and blocks when the semaphore is full.
//
//	var messages = tether.NewBus[MessageSent](tether.BusConfig{Topic: "messages"})
//	var local    = tether.NewBus[InternalEvent]()
func NewBus[E any](cfg ...BusConfig) *Bus[E] {
	var topic string
	workers := defaultAsyncWorkers
	overflow := Block
	if len(cfg) > 0 {
		c := cfg[0]
		topic = c.Topic
		if c.AsyncWorkers > 0 {
			workers = c.AsyncWorkers
		}
		if c.AsyncOverflow != 0 {
			overflow = c.AsyncOverflow
		}
	}
	if topic != "" {
		registerTopic(fmt.Sprintf("tether:bus:%s", topic))
	}
	b := &Bus[E]{
		sem:      make(chan struct{}, workers),
		overflow: overflow,
		topic:    topic,
	}
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
	b.initCluster()
	if em, ok := s.(emitter); ok {
		sid := em.sessionID()
		em.enqueue(func() {
			b.publish(event, sid)
			b.clusterPublish(event, sid)
		})
		return
	}
	// Partial session without emitter: synchronous publish.
	sid := s.ID()
	b.publish(event, sid)
	b.clusterPublish(event, sid)
}

// Publish sends an event to all subscribers with no sender filter.
// Use this for external event sources (database change listeners,
// message queue consumers, cron jobs) that have no session identity.
//
// Subscriber callbacks run synchronously in the caller's goroutine.
// Session-bound subscribers (registered via [On]) are non-blocking
// because they route through the session's command channel, but raw
// [Subscribe] callbacks that block will stall the caller.
//
// If the bus has a cluster topic and a cluster is configured, the
// event is also published to the cluster after local delivery.
func (b *Bus[E]) Publish(event E) {
	b.initCluster()
	b.publish(event, "")
	b.clusterPublish(event, "")
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
//
// A panic in the callback is recovered and logged; the publisher and
// the remaining subscribers are unaffected.
func (b *Bus[E]) Subscribe(ctx context.Context, fn func(E)) func() {
	return b.subscribe(ctx, fn, "")
}

// SubscribeAsync registers a callback that receives every event in its
// own goroutine. Concurrency is bounded by a semaphore (default 64
// workers, configurable via [BusConfig].AsyncWorkers). When all worker
// slots are occupied, the [BusConfig].AsyncOverflow strategy applies:
// [Block] (default) waits for a slot, [Drop] discards the event, and
// [Inline] runs the callback in the publisher's goroutine.
//
// Use this for external consumers that perform I/O (database writes,
// HTTP calls, logging) in response to events. For session-bound
// subscriptions, prefer [On] which routes through the session's
// command loop. The subscription lives until ctx is cancelled.
// Returns an unsubscribe function for early removal.
func (b *Bus[E]) SubscribeAsync(ctx context.Context, fn func(E)) func() {
	return b.subscribeAsync(ctx, fn, "")
}

// Len returns the number of registered subscribers. Lock-free. A
// subscription whose context has just been cancelled is still counted
// until its removal runs, so treat this as a close estimate rather than
// an exact live count.
func (b *Bus[E]) Len() int {
	return len(b.loadSubs())
}

// Close releases the bus's cluster subscription, if any. Package-level
// buses live for the whole process and never need this; call it when
// a Bus with a cluster topic is created per-request or per-entity,
// which would otherwise leak its broker subscription and goroutine.
func (b *Bus[E]) Close() {
	// The no-op Do synchronises with a concurrent lazy cluster
	// subscribe - it blocks until any in-flight first Do completes,
	// making the clusterUnsub read safe.
	b.clusterOnce.Do(func() {})
	if b.clusterUnsub != nil {
		b.clusterUnsub()
	}
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
	// A nil context would panic in context.AfterFunc below and again
	// in the lock-free publish path. Treat nil as "never cancelled".
	if ctx == nil {
		ctx = context.Background()
	}
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
// senderID are skipped. Async subscribers are dispatched via the
// bounded semaphore.
func (b *Bus[E]) publish(event E, senderID string) {
	for _, s := range b.loadSubs() {
		if s.ctx.Err() != nil {
			continue // dead subscriber, skip
		}
		if senderID != "" && s.sessionID == senderID {
			continue // sender filtering
		}
		if s.async {
			b.dispatchAsync(s.fn, event)
		} else {
			b.dispatchSync(s.fn, event)
		}
	}
}

// dispatchSync runs a synchronous callback in the publisher's
// goroutine. The panic recovery matches dispatchAsync: one broken
// subscriber must not take down the publisher, which for [Bus.Publish]
// is often an unrelated goroutine (a change listener, a queue consumer)
// with no recovery of its own. Remaining subscribers still receive the
// event.
func (b *Bus[E]) dispatchSync(fn func(E), event E) {
	defer func() {
		if r := recover(); r != nil {
			dev.Log().Error("panic in bus subscriber", "panic", r)
		}
	}()
	fn(event)
}

// dispatchAsync runs an async callback with semaphore bounding. The
// fast path acquires a slot without blocking. When the semaphore is
// full, the configured overflow strategy applies.
func (b *Bus[E]) dispatchAsync(fn func(E), event E) {
	wrapped := func() {
		defer func() {
			if r := recover(); r != nil {
				dev.Log().Error("panic in bus subscriber", "panic", r)
			}
		}()
		fn(event)
	}
	select {
	case b.sem <- struct{}{}:
		go func() {
			defer func() { <-b.sem }()
			wrapped()
		}()
	default:
		switch b.overflow {
		case Drop:
			dev.Log().Warn("tether: async event dropped, semaphore full")
		case Inline:
			wrapped()
		default: // Block
			b.sem <- struct{}{}
			go func() {
				defer func() { <-b.sem }()
				wrapped()
			}()
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
