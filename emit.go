package tether

import "github.com/jpl-au/fluent-tether/dev"

// On subscribes a session to a typed event bus. When the bus publishes
// an event, fn is called inside the session's command loop (via
// [LiveSession.Update]) with the event and the current state. The
// callback returns the new state — same pattern as Update.
//
// Sender filtering is automatic: if the event was emitted by this
// session (via [Bus.Emit]), the callback is skipped. This prevents
// double-apply — Handle updates the sender's state directly, the bus
// updates everyone else.
//
// The subscription is cleaned up automatically when the session is
// destroyed (context cancelled). No manual unsubscribe needed.
//
// On is a top-level function rather than a Bus method because it needs
// two type parameters (E for the event, S for the state). Go methods
// cannot introduce additional type parameters.
//
//	tether.On(s, messages, func(ev MessageSent, state ChatState) ChatState {
//	    state.Messages = append(state.Messages, ev.Text)
//	    return state
//	})
func On[E any, S any](s *LiveSession[S], bus *Bus[E], fn func(E, S) S) {
	dev.Debug("bus.on", "session", s.ID(), "endpoint", s.endpoint)
	bus.subscribe(s.Context(), func(ev E) {
		s.Update(func(state S) S {
			return fn(ev, state)
		})
	}, s.ID())
}
