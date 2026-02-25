package poly

import "sync"

// Value is a thread-safe container for shared state that notifies
// observers when it changes. Built on top of [Bus] internally — when
// Set or Update is called, the new value is published to all observers
// registered via [Observe].
//
// Use Value for state that multiple sessions need to stay in sync with
// (online counts, shared configuration, room membership). For discrete
// domain events, use [Bus] directly.
//
//	var onlineCount = poly.NewValue(0)
//
//	// From any goroutine:
//	onlineCount.Update(func(n int) int { return n + 1 })
//
//	// In OnConnect:
//	poly.Observe(onlineCount, s, func(count int, state State) State {
//	    state.OnlineUsers = count
//	    return state
//	})
type Value[V any] struct {
	mu  sync.RWMutex
	val V
	bus *Bus[V]
}

// NewValue creates a Value with an initial state.
func NewValue[V any](initial V) *Value[V] {
	return &Value[V]{
		val: initial,
		bus: NewBus[V](),
	}
}

// Get returns the current value.
func (v *Value[V]) Get() V {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.val
}

// Set writes a new value and publishes it to all observers.
func (v *Value[V]) Set(val V) {
	v.mu.Lock()
	v.val = val
	v.mu.Unlock()
	v.bus.Publish(val)
}

// Update performs an atomic read-modify-write. It reads the current
// value, applies fn, writes the result, and publishes it to all
// observers. Useful for counters and accumulators where the new value
// depends on the old.
func (v *Value[V]) Update(fn func(V) V) {
	v.mu.Lock()
	v.val = fn(v.val)
	current := v.val
	v.mu.Unlock()
	v.bus.Publish(current)
}

// Len returns the number of active observers.
func (v *Value[V]) Len() int {
	return v.bus.Len()
}
