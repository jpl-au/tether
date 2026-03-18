package tether

import (
	"context"
	"sync"
	"sync/atomic"
)

// Value is a thread-safe container for shared state that notifies
// observers when it changes. Built on top of [Bus] internally — when
// Store or Update is called, the new value is published to all
// observers registered via [Observe].
//
// Load is lock-free (atomic load). Store and Update serialise writes
// under a mutex and store the new value atomically, so concurrent
// readers never block.
//
// Use Value for state that multiple sessions need to stay in sync with
// (online counts, shared configuration, room membership). For discrete
// domain events, use [Bus] directly.
//
//	var onlineCount = tether.NewValue(0)
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
}

// valueBox wraps the stored value so atomic.Value.Store never receives
// a nil interface — which would panic. The box is always non-nil even
// when V itself is nil (e.g. Value[*Foo] with a nil pointer).
type valueBox[V any] struct{ v V }

// NewValue creates a Value with an initial state.
func NewValue[V any](initial V) *Value[V] {
	v := &Value[V]{bus: NewBus[V]()}
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

// Store writes a new value and publishes it to all observers.
func (v *Value[V]) Store(val V) {
	v.wmu.Lock()
	v.val.Store(valueBox[V]{val})
	v.wmu.Unlock()
	v.bus.Publish(val)
}

// Update performs an atomic read-modify-write. It reads the current
// value, applies fn, writes the result, and publishes it to all
// observers. Useful for counters and accumulators where the new value
// depends on the old.
func (v *Value[V]) Update(fn func(V) V) {
	v.wmu.Lock()
	current := fn(v.Load())
	v.val.Store(valueBox[V]{current})
	v.wmu.Unlock()
	v.bus.Publish(current)
}

// Len returns the number of active observers.
func (v *Value[V]) Len() int {
	return v.bus.Len()
}

// observe registers a subscriber and returns the current value
// atomically. The write mutex is held during both the read and the
// subscribe so a concurrent Set cannot publish between the two —
// preventing duplicate delivery of the initial value. Get() callers
// are unaffected because reads are lock-free.
func (v *Value[V]) observe(ctx context.Context, fn func(V), sessionID string) V {
	v.wmu.Lock()
	current := v.Load()
	v.bus.subscribe(ctx, fn, sessionID)
	v.wmu.Unlock()
	return current
}
