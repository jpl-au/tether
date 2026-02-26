package poly

import (
	"iter"
	"sync"
	"sync/atomic"
)

// Group tracks a set of sessions for broadcasting state updates.
// Add sessions in OnConnect and remove them in OnDisconnect, or use
// [Config].Groups for automatic membership:
//
//	group := poly.NewGroup[State]()
//
//	OnConnect: func(s *poly.Session[State]) {
//	    group.Add(s)
//	},
//	OnDisconnect: func(s *poly.Session[State]) {
//	    group.Remove(s)
//	},
//
//	// Later, push an update to every session in the group:
//	group.Broadcast(func(target *poly.Session[State], state State) State {
//	    state.Message = "Hello everyone"
//	    return state
//	})
//
// The session map is stored in an [atomic.Value] so Broadcast (the hot
// path) is completely lock-free. Add and Remove use a write mutex with
// copy-on-write semantics.
type Group[S any] struct {
	// wmu serialises writes (Add/Remove). Reads go through the
	// atomic.Value and need no lock.
	wmu      sync.Mutex
	sessions atomic.Value // holds map[string]*Session[S]

	// OnJoin is called after a session is added to the group.
	// Runs outside the write lock so it is safe to call Broadcast
	// or other Group methods from within. Optional.
	OnJoin func(session *Session[S])

	// OnLeave is called after a session is removed from the group.
	// Runs outside the write lock so it is safe to call Broadcast
	// or other Group methods from within. Optional.
	OnLeave func(session *Session[S])
}

// NewGroup creates an empty group ready to accept sessions.
// Typically called once at program startup and shared across the
// OnConnect/OnDisconnect callbacks and any code that broadcasts.
func NewGroup[S any]() *Group[S] {
	g := &Group[S]{}
	g.sessions.Store(make(map[string]*Session[S]))
	return g
}

// Add registers a session with the group. If the session is new and
// OnJoin is set, the callback fires after the session is added.
// Safe to call from any goroutine. Adding a session that is already
// in the group is a no-op (OnJoin does not fire).
func (g *Group[S]) Add(s *Session[S]) {
	g.wmu.Lock()
	old := g.loadSessions()
	_, exists := old[s.id]
	if !exists {
		sessions := make(map[string]*Session[S], len(old)+1)
		for k, v := range old {
			sessions[k] = v
		}
		sessions[s.id] = s
		g.sessions.Store(sessions)
	}
	onJoin := g.OnJoin
	g.wmu.Unlock()

	if !exists && onJoin != nil {
		onJoin(s)
	}
}

// Remove unregisters a session from the group. If the session was
// present and OnLeave is set, the callback fires after removal.
// Safe to call from any goroutine. Removing a session that is not
// in the group is a no-op (OnLeave does not fire).
func (g *Group[S]) Remove(s *Session[S]) {
	g.wmu.Lock()
	old := g.loadSessions()
	_, exists := old[s.id]
	if exists {
		sessions := make(map[string]*Session[S], len(old))
		for k, v := range old {
			if k != s.id {
				sessions[k] = v
			}
		}
		g.sessions.Store(sessions)
	}
	onLeave := g.OnLeave
	g.wmu.Unlock()

	if exists && onLeave != nil {
		onLeave(s)
	}
}

// Len returns the number of sessions currently in the group. Useful
// for displaying an "N users online" indicator via [Session.Update].
// Lock-free.
func (g *Group[S]) Len() int {
	return len(g.loadSessions())
}

// All returns an iterator over sessions in the group. The map is
// read via a single atomic load — no lock is held during iteration,
// so it is safe to call Add, Remove, or Broadcast from within the
// loop body.
func (g *Group[S]) All() iter.Seq[*Session[S]] {
	return func(yield func(*Session[S]) bool) {
		for _, s := range g.loadSessions() {
			if !yield(s) {
				return
			}
		}
	}
}

// Broadcast applies fn to every session in the group. The callback
// receives the target session so side-effect methods (Toast, Navigate,
// etc.) are called on the correct session. Each session's Update is
// non-blocking — it queues a command on the session's channel — so
// Broadcast does not spawn goroutines per session.
//
// Safe to call from any goroutine, including from within Handle.
func (g *Group[S]) Broadcast(fn func(target *Session[S], state S) S) {
	for _, t := range g.loadSessions() {
		t.Update(func(state S) S {
			return fn(t, state)
		})
	}
}

// BroadcastOthers applies fn to every session in the group except
// the excluded one. This is the typical pattern when broadcasting from
// inside [HandleFunc]: Handle updates the sender's state directly
// (via the return value) and uses BroadcastOthers to push the change
// to everyone else, avoiding a double-apply on the sender.
//
// Safe to call from any goroutine, including from within Handle.
func (g *Group[S]) BroadcastOthers(exclude *Session[S], fn func(target *Session[S], state S) S) {
	for _, t := range g.loadSessions() {
		if t != exclude {
			t.Update(func(state S) S {
				return fn(t, state)
			})
		}
	}
}

// loadSessions returns the current session map from the atomic.Value.
func (g *Group[S]) loadSessions() map[string]*Session[S] {
	return g.sessions.Load().(map[string]*Session[S])
}
