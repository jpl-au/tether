package poly

import (
	"sync"
)

// Group tracks a set of sessions for broadcasting state updates.
// Add sessions in OnConnect and remove them in OnDisconnect:
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
//	group.Broadcast(func(state State) State {
//	    state.Message = "Hello everyone"
//	    return state
//	})
type Group[S any] struct {
	mu       sync.Mutex
	sessions map[string]*Session[S]
}

// NewGroup creates an empty group ready to accept sessions.
// Typically called once at program startup and shared across the
// OnConnect/OnDisconnect callbacks and any code that broadcasts.
func NewGroup[S any]() *Group[S] {
	return &Group[S]{
		sessions: make(map[string]*Session[S]),
	}
}

// Add registers a session with the group. Safe to call from any
// goroutine. Adding a session that is already in the group is a no-op.
func (g *Group[S]) Add(s *Session[S]) {
	g.mu.Lock()
	g.sessions[s.id] = s
	g.mu.Unlock()
}

// Remove unregisters a session from the group. Safe to call from any
// goroutine. Removing a session that is not in the group is a no-op.
func (g *Group[S]) Remove(s *Session[S]) {
	g.mu.Lock()
	delete(g.sessions, s.id)
	g.mu.Unlock()
}

// Len returns the number of sessions currently in the group. Useful
// for displaying an "N users online" indicator via [Session.Update].
func (g *Group[S]) Len() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sessions)
}

// Broadcast applies fn to every session in the group via
// [Session.Update]. Each session is updated in its own goroutine so
// a slow render in one session does not block delivery to the rest.
//
// Broadcast does not wait for the updates to complete. This is
// necessary because Broadcast is typically called from inside a
// [HandleFunc] (e.g. chat messages, collaborative edits), where the
// calling session's mutex is held. If Broadcast blocked, the update
// goroutine for the calling session would deadlock trying to acquire
// the same mutex. The goroutines are bounded by the number of sessions
// in the group and each completes after a single render-diff-send
// cycle, so they do not accumulate.
//
// Safe to call from any goroutine.
func (g *Group[S]) Broadcast(fn func(S) S) {
	g.mu.Lock()
	targets := make([]*Session[S], 0, len(g.sessions))
	for _, s := range g.sessions {
		targets = append(targets, s)
	}
	g.mu.Unlock()

	for _, s := range targets {
		go s.Update(fn)
	}
}
