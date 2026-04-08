package tether

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Value is a thread-safe container for shared state that notifies
// observers when it changes. Built on top of [Bus] internally - when
// Store or Update is called, the new value is published to all
// observers registered via [Observe].
//
// Load is lock-free (atomic load). Store and Update serialise writes
// under a mutex and store the new value atomically, so concurrent
// readers never block.
//
// When created with an optional topic name, Value publishes changes to
// the cluster (if configured on [App]) and receives changes from other
// nodes. Without a topic, Value is local-only.
//
// Use Value for state that multiple sessions need to stay in sync with
// (online counts, shared configuration, room membership). For discrete
// domain events, use [Bus] directly.
//
//	var onlineCount = tether.NewValue(0, "online-count")
//
//	// From any goroutine:
//	onlineCount.Update(func(n int) int { return n + 1 })
//
//	// In OnConnect:
//	tether.Observe(s, onlineCount, func(count int, state State) State {
//	    state.OnlineUsers = count
//	    return state
//	})
type Value[V any] struct {
	// wmu serialises writes (Set, Update, observe). Reads go through
	// the atomic.Value and need no lock.
	wmu sync.Mutex
	val atomic.Value // holds valueBox[V]
	bus *Bus[V]

	// topic is the cluster topic name. Empty means local-only.
	topic string

	// clusterOnce ensures we subscribe to the cluster topic at most
	// once, lazily on first Store or Update.
	clusterOnce  sync.Once
	clusterUnsub func()
}

// valueBox wraps the stored value so atomic.Value.Store never receives
// a nil interface - which would panic. The box is always non-nil even
// when V itself is nil (e.g. Value[*Foo] with a nil pointer).
type valueBox[V any] struct{ v V }

// NewValue creates a Value with an initial state. An optional topic
// name enables cluster synchronisation - when provided, Store and
// Update publish changes to other nodes. Without a topic, the Value
// is local-only.
//
//	var onlineCount = tether.NewValue(0, "online-count")
//	var localState  = tether.NewValue(State{})
func NewValue[V any](initial V, topic ...string) *Value[V] {
	t := ""
	if len(topic) > 0 {
		t = topic[0]
	}
	if t != "" {
		registerTopic(fmt.Sprintf("tether:value:%s", t))
	}
	v := &Value[V]{
		bus:   NewBus[V](),
		topic: t,
	}
	v.val.Store(valueBox[V]{initial})
	return v
}

// Load returns the current value. Lock-free. Returns the zero value
// of V if the Value was not created via [NewValue].
func (v *Value[V]) Load() V {
	raw := v.val.Load()
	if raw == nil {
		var zero V
		return zero
	}
	return raw.(valueBox[V]).v
}

// Store writes a new value and publishes it to all observers. If the
// Value has a cluster topic and a cluster is configured, the change
// is also published to other nodes.
func (v *Value[V]) Store(val V) {
	v.initCluster()
	v.storeLocal(val)
	v.clusterPublish(val)
}

// Update performs an atomic read-modify-write. It reads the current
// value, applies fn, writes the result, and publishes it to all
// observers. Useful for counters and accumulators where the new value
// depends on the old. If the Value has a cluster topic and a cluster
// is configured, the change is also published to other nodes.
func (v *Value[V]) Update(fn func(V) V) {
	v.initCluster()
	v.wmu.Lock()
	current := fn(v.Load())
	v.val.Store(valueBox[V]{current})
	v.wmu.Unlock()
	v.bus.Publish(current)
	v.clusterPublish(current)
}

// storeLocal writes a new value and publishes to local observers
// without publishing to the cluster. Used by the cluster subscription
// handler to prevent infinite loops.
func (v *Value[V]) storeLocal(val V) {
	v.wmu.Lock()
	v.val.Store(valueBox[V]{val})
	v.wmu.Unlock()
	v.bus.Publish(val)
}

// Len returns the number of active observers.
func (v *Value[V]) Len() int {
	return v.bus.Len()
}

// observe registers a subscriber and returns the current value
// atomically. The write mutex is held during both the read and the
// subscribe so a concurrent Set cannot publish between the two  -
// preventing duplicate delivery of the initial value. Get() callers
// are unaffected because reads are lock-free.
func (v *Value[V]) observe(ctx context.Context, fn func(V), sessionID string) V {
	v.initCluster()
	v.wmu.Lock()
	current := v.Load()
	v.bus.subscribe(ctx, fn, sessionID)
	v.wmu.Unlock()
	return current
}
