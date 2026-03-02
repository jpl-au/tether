package poly

import "log/slog"

// Observe subscribes a session to a shared [Value]. The callback
// receives the shared value and the session's current state, and
// returns the new state — same shape as [On].
//
// The current value is delivered immediately so the session's state
// is up to date from the moment of subscription. Future changes via
// [Value.Store] or [Value.Update] are delivered automatically.
//
// The subscription and initial value read happen atomically — a
// concurrent Set cannot slip between the two and cause duplicate
// delivery of the same value.
//
// The subscription is cleaned up when the session is destroyed.
//
//	poly.Observe(onlineCount, s, func(count int, state State) State {
//	    state.OnlineUsers = count
//	    return state
//	})
func Observe[V any, S any](val *Value[V], s *Session[S], fn func(V, S) S) {
	slog.Debug("observe.subscribe", "session", s.ID(), "endpoint", s.endpoint)
	// Subscribe and read the current value under the Value's lock so
	// a concurrent Store cannot interleave and deliver the same value
	// via both the subscriber callback and the initial Update below.
	current := val.observe(s.Context(), func(v V) {
		s.Update(func(state S) S {
			return fn(v, state)
		})
	}, s.ID())

	// Deliver current value so the session is immediately in sync.
	s.Update(func(state S) S {
		return fn(current, state)
	})
}
