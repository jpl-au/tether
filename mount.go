package tether

import "strings"

// ComponentMount wires a [Component] into the session's event dispatch.
// Create mounts with [Mount] and list them in [Config.Components].
//
// ComponentMount has an unexported method so the framework controls
// dispatch — callers cannot implement this interface directly.
type ComponentMount[S any] interface {
	route(sess Session, state S, ev Event) (S, bool)
}

// Mount creates a [ComponentMount] that wires a [Component]-implementing
// type into the session's event dispatch. The prefix identifies the
// component's event namespace — events with actions starting with
// "prefix." are routed to the component. The getter extracts the
// component from S; the setter writes the updated component back.
//
// This follows the same pattern as [WatchValue] and [WatchBus]: a
// generic constructor that returns a non-generic interface, allowing
// Config.Components to hold mounts for different component types.
//
//	tether.Config[State]{
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

type componentMount[S any, C Component] struct {
	prefix string
	getter func(S) C
	setter func(S, C) S
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
