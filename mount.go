package tether

import "strings"

// ComponentMount wires a [Component] into the session's event dispatch.
// Create mounts with [Mount] and list them in [LiveConfig.Components].
//
// ComponentMount has unexported methods so the framework controls
// dispatch — callers cannot implement this interface directly.
type ComponentMount[S any] interface {
	route(sess Session, state S, ev Event) (S, bool)

	// init calls [Mounter].Mount on the component if it implements
	// the [Mounter] interface, writing the result back into state.
	// Components that do not implement Mounter are left unchanged.
	init(sess Session, state S) S
}

// Mount creates a [ComponentMount] that wires a [Component]-implementing
// type into the session's event dispatch. The prefix identifies the
// component's event namespace — events with actions starting with
// "prefix." are routed to the component. The getter extracts the
// component from S; the setter writes the updated component back.
//
// This follows the same pattern as [WatchValue] and [WatchBus]: a
// generic constructor that returns a non-generic interface, allowing
// LiveConfig.Components to hold mounts for different component types.
//
//	tether.LiveConfig[State]{
//	    Components: []tether.ComponentMount[State]{
//	        tether.Mount("chat",
//	            func(s State) chatwidget.Widget { return s.Chat },
//	            func(s State, c chatwidget.Widget) State { s.Chat = c; return s },
//	        ),
//	    },
//	}
func Mount[S any, C Component](prefix string, getter func(S) C, setter func(S, C) S) ComponentMount[S] {
	return &componentMount[S, C]{
		prefix: prefix,
		getter: getter,
		setter: setter,
	}
}

// RouteMount tries each mount in order. If one matches, it returns the
// updated state and true. Otherwise it returns the original state and
// false. Used by the exec loop and tethertest to dispatch component
// events before the user's Handle function.
func RouteMount[S any](mounts []ComponentMount[S], sess Session, state S, ev Event) (S, bool) {
	for _, m := range mounts {
		if s, ok := m.route(sess, state, ev); ok {
			return s, true
		}
	}
	return state, false
}

// InitMounts calls [Mounter].Mount on each mounted component that
// implements the [Mounter] interface. Called once per session after the
// command loop starts. Components that do not implement Mounter are
// left unchanged.
func InitMounts[S any](mounts []ComponentMount[S], sess Session, state S) S {
	for _, m := range mounts {
		state = m.init(sess, state)
	}
	return state
}

type componentMount[S any, C Component] struct {
	prefix string
	getter func(S) C
	setter func(S, C) S
}

// init calls Mounter.Mount on the component if it implements the
// Mounter interface. This runs once per session during startup so
// components can perform initial side effects (Toast, Signal, Go).
func (m *componentMount[S, C]) init(sess Session, state S) S {
	comp := m.getter(state)
	if mounter, ok := any(comp).(Mounter); ok {
		updated := mounter.Mount(sess).(C)
		return m.setter(state, updated)
	}
	return state
}

// route checks whether the event matches this mount's prefix. If it
// does, the component is extracted from state, Handle is called with
// the prefix-stripped event, and the updated component is written back.
// Returns the updated state and true if the event was handled, or the
// original state and false if the prefix didn't match.
func (m *componentMount[S, C]) route(sess Session, state S, ev Event) (S, bool) {
	target := m.prefix + "."
	if !strings.HasPrefix(ev.Action, target) {
		return state, false
	}
	comp := m.getter(state)
	scoped := ev.WithAction(strings.TrimPrefix(ev.Action, target))
	scoped.Target = m.prefix
	updated := comp.Handle(sess, scoped).(C)
	return m.setter(state, updated), true
}
