package tether

import "github.com/jpl-au/tether/dev"

// Observe subscribes a session to a shared [Value]. The callback
// receives the shared value and the session's current state, and
// returns the new state - same shape as [On].
//
// The current value is delivered immediately so the session's state
// is up to date from the moment of subscription. Future changes via
// [Value.Store] or [Value.Update] are delivered automatically.
//
// The subscription, initial read, and initial state application all
// happen within a single session command. A concurrent [Value.Store]
// that arrives after the subscription is registered will always be
// ordered after the initial value - the session never sees a stale
// overwrite.
//
// The subscription is cleaned up when the session is destroyed.
//
//	tether.Observe(s, onlineCount, func(count int, state State) State {
//	    state.OnlineUsers = count
//	    return state
//	})
func Observe[V any, S any](s *StatefulSession[S], val *Value[V], fn func(V, S) S) {
	dev.Debug("observe.subscribe", "session", s.ID(), "endpoint", s.endpoint)
	// Subscribe, read, and apply the current value inside a single
	// Update so there is no gap between "subscribed" and "initial
	// value delivered." Any concurrent Store that fires the subscriber
	// callback enqueues its Update after this one, preserving order.
	s.Update(func(state S) S {
		current := val.observe(s.Context(), func(v V) {
			s.Update(func(inner S) S {
				return fn(v, inner)
			})
		}, s.ID())
		return fn(current, state)
	})
}
