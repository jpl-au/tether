package tether

// Scope focuses a [Session]'s state onto a smaller component type.
// View extracts the component state from the full session state;
// Update injects a modified component state back. Handlers and render
// functions that work through a Scope only see the component type C
// — never the full state S.
//
// Define scopes as package-level variables:
//
//	var todos = tether.Scope[AppState, TodoState]{
//	    View:   func(s AppState) TodoState { return s.Todos },
//	    Update: func(s AppState, c TodoState) AppState { s.Todos = c; return s },
//	}
type Scope[S, C any] struct {
	View   func(S) C
	Update func(S, C) S
}

// Handle dispatches an event to a component handler that only sees
// the component's sub-state. The handler receives [Session] for
// side effects (Toast, Navigate, Signal, etc.) but cannot access the
// full session state — true encapsulation.
//
// Call this from within the main [HandleFunc]:
//
//	func handle(sess *tether.LiveSession[AppState], state AppState, ev tether.Event) AppState {
//	    return todos.Handle(sess, state, ev, todoHandle)
//	}
//
//	func todoHandle(sess tether.Session, ts TodoState, ev tether.Event) TodoState {
//	    switch ev.Action {
//	    case "todo.add":
//	        ts.Items = append(ts.Items, Todo{Text: ev.Value()})
//	        sess.Toast("Added")
//	    }
//	    return ts
//	}
func (sc Scope[S, C]) Handle(sess Session, state S, ev Event, fn func(Session, C, Event) C) S {
	return sc.Update(state, fn(sess, sc.View(state), ev))
}

// With applies a pure transformation to the component's sub-state.
// Use this inside [LiveSession.Update] callbacks for server-initiated
// changes:
//
//	sess.Update(func(state AppState) AppState {
//	    return todos.With(state, func(ts TodoState) TodoState {
//	        ts.Count++
//	        return ts
//	    })
//	})
func (sc Scope[S, C]) With(state S, fn func(C) C) S {
	return sc.Update(state, fn(sc.View(state)))
}
