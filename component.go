package tether

import (
	"strings"

	"github.com/jpl-au/fluent/node"
)

// Component is a self-contained rendering unit with its own state. A
// component knows how to render itself and handle its own events, without
// any knowledge of the parent's state type S.
//
// Components are value types. Handle returns a new Component value after
// each event — the receiver is not mutated. This matches the HandleFunc
// pattern (returns new S) and is critical for the diff engine: the parent
// stores the returned Component in S, the next render calls Render on the
// new value, and the diff detects changes.
//
// A component struct implements this interface by embedding its own state
// as fields. Library authors publish components as concrete structs that
// satisfy Component — callers store the concrete type in their state for
// full compile-time safety via [RouteTyped].
//
// Components only receive [Session] (not [*LiveSession]), so they work
// during SSR pre-warming (captureSession satisfies Session) and in tests
// (tethertest.NewSession satisfies Session) without special cases.
type Component interface {
	// Render builds the component's node tree. The root node should
	// have a stable Dynamic key so the diff engine can track it across
	// renders. Without a Dynamic key, changes to the component produce
	// no patches and the client never updates.
	Render() node.Node

	// Handle processes an event and returns the updated component. If
	// the event is not relevant, return the receiver unchanged — the
	// parent's Equal check will detect no change and skip the diff.
	//
	// Handle runs inside the session's command loop. Keep it fast — no
	// blocking I/O, no sleeps, no channel waits. Use [Session.Go] for
	// slow work.
	Handle(Session, Event) Component
}

// EqualComponent is an optional interface that components can implement
// to provide fast equality checking. The parent's Equal function (or the
// framework's default comparison) checks for this interface before
// falling back to reflect.DeepEqual or byte-level diffing.
//
// Implement this when your component contains slices, maps, or other
// fields that make reflect.DeepEqual expensive. Simple components with
// only scalar fields can rely on the default comparison.
//
// When a Component is stored as an interface field (Approach B), Go's ==
// operator cannot compare interface values containing slices or maps —
// it panics. EqualComponent is load-bearing in that scenario, not
// optional.
type EqualComponent interface {
	Component
	EqualComponent(Component) bool
}

// Route dispatches an event to a component by prefix. If the event's
// action starts with "prefix.", the prefix is stripped and the event is
// forwarded to comp.Handle. Otherwise the component is returned unchanged.
//
// Route returns the Component interface type. For typed field access in
// the parent's state, use [RouteTyped] instead.
//
//	func handle(sess tether.Session, s State, ev tether.Event) State {
//	    s.Chat = tether.Route(s.Chat, "chat", sess, ev)
//	    return s
//	}
func Route(comp Component, prefix string, sess Session, ev Event) Component {
	target := prefix + "."
	if !strings.HasPrefix(ev.Action, target) {
		return comp
	}
	return comp.Handle(sess, ev.WithAction(strings.TrimPrefix(ev.Action, target)))
}

// RouteTyped dispatches an event to a component by prefix, preserving the
// concrete type through the Component interface dispatch. This allows the
// parent to store the concrete component type in its state struct — full
// compile-time safety, direct field access, no type assertions needed.
//
// The type assertion comp.Handle(...).(C) is safe because a Component's
// Handle method returns the same concrete type as its receiver. This is a
// contract of the pattern — a Widget.Handle always returns a Widget. If a
// Handle implementation returns a different concrete type, RouteTyped
// panics. In debug/dev mode, the panic message identifies the mismatch.
//
//	type State struct {
//	    Chat chatwidget.Widget // concrete type, not tether.Component
//	}
//
//	func handle(sess tether.Session, s State, ev tether.Event) State {
//	    s.Chat = tether.RouteTyped(s.Chat, "chat", sess, ev)
//	    return s
//	}
func RouteTyped[C Component](comp C, prefix string, sess Session, ev Event) C {
	target := prefix + "."
	if !strings.HasPrefix(ev.Action, target) {
		return comp
	}
	return comp.Handle(sess, ev.WithAction(strings.TrimPrefix(ev.Action, target))).(C)
}
