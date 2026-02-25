package poly

// Observe subscribes a session to a shared [Value]. The callback
// receives the shared value and the session's current state, and
// returns the new state — same shape as [On].
//
// The current value is delivered immediately so the session's state
// is up to date from the moment of subscription. Future changes via
// [Value.Set] or [Value.Update] are delivered automatically.
//
// The subscription is cleaned up when the session is destroyed.
//
//	poly.Observe(onlineCount, s, func(count int, state State) State {
//	    state.OnlineUsers = count
//	    return state
//	})
func Observe[V any, S any](val *Value[V], s *Session[S], fn func(V, S) S) {
	// Subscribe to future changes via the internal bus.
	On(val.bus, s, func(v V, state S) S {
		return fn(v, state)
	})

	// Deliver current value so the session is immediately in sync.
	current := val.Get()
	s.Update(func(state S) S {
		return fn(current, state)
	})
}
