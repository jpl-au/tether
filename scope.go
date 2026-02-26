package poly

// Scope focuses a [Session]'s state onto a smaller component type.
// Get extracts the component state from the full session state; Set
// injects a modified component state back. Handlers and render
// functions that work through a Scope only see the component type C
// — never the full state S.
//
// Define scopes as package-level variables:
//
//	var todos = poly.Scope[AppState, TodoState]{
//	    Get: func(s AppState) TodoState { return s.Todos },
//	    Set: func(s AppState, c TodoState) AppState { s.Todos = c; return s },
//	}
type Scope[S, C any] struct {
	Get func(S) C
	Set func(S, C) S
}

// Handle dispatches an event to a component handler that only sees
// the component's sub-state. The handler receives [PreSession] for
// side effects (Toast, Navigate, Signal, etc.) but cannot access the
// full session state — true encapsulation.
//
// Call this from within the main [HandleFunc]:
//
//	func handle(sess *poly.Session[AppState], state AppState, ev poly.Event) AppState {
//	    return todos.Handle(sess, state, ev, todoHandle)
//	}
//
//	func todoHandle(sess poly.PreSession, ts TodoState, ev poly.Event) TodoState {
//	    switch ev.Action {
//	    case "todo.add":
//	        ts.Items = append(ts.Items, Todo{Text: ev.Value()})
//	        sess.Toast("Added")
//	    }
//	    return ts
//	}
func (sc Scope[S, C]) Handle(sess PreSession, state S, ev Event, fn func(PreSession, C, Event) C) S {
	return sc.Set(state, fn(sess, sc.Get(state), ev))
}

// With applies a pure transformation to the component's sub-state.
// Use this inside [Session.Update] callbacks for server-initiated
// changes:
//
//	sess.Update(func(state AppState) AppState {
//	    return todos.With(state, func(ts TodoState) TodoState {
//	        ts.Count++
//	        return ts
//	    })
//	})
func (sc Scope[S, C]) With(state S, fn func(C) C) S {
	return sc.Set(state, fn(sc.Get(state)))
}
