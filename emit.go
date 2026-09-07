package tether

import "github.com/jpl-au/tether/dev"

// On subscribes a session to a typed event bus. When the bus publishes
// an event, fn is called inside the session's command loop (via
// [StatefulSession.Update]) with the event and the current state. The
// callback returns the new state - same pattern as Update.
//
// Sender filtering is automatic: if the event was emitted by this
// session (via [Bus.Emit]), the callback is skipped. This prevents
// double-apply - Handle updates the sender's state directly, the bus
// updates everyone else.
//
// The subscription is cleaned up automatically when the session is
// frozen or destroyed. It survives ordinary transport loss. Re-register
// imperative subscriptions in OnRestore (or its OnConnect fallback);
// declarative Watchers are re-registered automatically on thaw.
//
// On is a top-level function rather than a Bus method because it needs
// two type parameters (E for the event, S for the state). Go methods
// cannot introduce additional type parameters.
//
//	tether.On(s, messages, func(ev MessageSent, state ChatState) ChatState {
//	    state.Messages = append(state.Messages, ev.Text)
//	    return state
//	})
func On[E any, S any](s *StatefulSession[S], bus *Bus[E], fn func(E, S) S) {
	dev.Debug("bus.on", "session", s.ID(), "endpoint", s.endpoint)
	ctx := s.subscriptionContext()
	bus.subscribe(ctx, func(ev E) {
		s.Update(func(state S) S {
			if ctx.Err() != nil {
				return state
			}
			return fn(ev, state)
		})
	}, s.ID())
}
