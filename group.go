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
//	group.Broadcast(func(state State) poly.HandleResult[State] {
//	    state.Message = "Hello everyone"
//	    return poly.Result(state)
//	})
type Group[S any] struct {
	mu       sync.Mutex
	sessions map[string]*Session[S]

	// OnJoin is called after a session is added to the group.
	// Runs outside the group lock so it is safe to call Broadcast
	// or other Group methods from within. Optional.
	OnJoin func(session *Session[S])

	// OnLeave is called after a session is removed from the group.
	// Runs outside the group lock so it is safe to call Broadcast
	// or other Group methods from within. Optional.
	OnLeave func(session *Session[S])
}

// NewGroup creates an empty group ready to accept sessions.
// Typically called once at program startup and shared across the
// OnConnect/OnDisconnect callbacks and any code that broadcasts.
func NewGroup[S any]() *Group[S] {
	return &Group[S]{
		sessions: make(map[string]*Session[S]),
	}
}

// Add registers a session with the group. If the session is new and
// OnJoin is set, the callback fires after the session is added.
// Safe to call from any goroutine. Adding a session that is already
// in the group is a no-op (OnJoin does not fire).
func (g *Group[S]) Add(s *Session[S]) {
	g.mu.Lock()
	_, exists := g.sessions[s.id]
	g.sessions[s.id] = s
	onJoin := g.OnJoin
	g.mu.Unlock()

	if !exists && onJoin != nil {
		onJoin(s)
	}
}

// Remove unregisters a session from the group. If the session was
// present and OnLeave is set, the callback fires after removal.
// Safe to call from any goroutine. Removing a session that is not
// in the group is a no-op (OnLeave does not fire).
func (g *Group[S]) Remove(s *Session[S]) {
	g.mu.Lock()
	_, exists := g.sessions[s.id]
	delete(g.sessions, s.id)
	onLeave := g.OnLeave
	g.mu.Unlock()

	if exists && onLeave != nil {
		onLeave(s)
	}
}

// Len returns the number of sessions currently in the group. Useful
// for displaying an "N users online" indicator via [Session.Update].
func (g *Group[S]) Len() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sessions)
}

// Members returns a snapshot of the sessions currently in the group.
// The returned slice is safe to iterate without holding the group lock.
// Use [Session.State] to read each session's state (e.g. for building
// a list of online usernames).
func (g *Group[S]) Members() []*Session[S] {
	g.mu.Lock()
	defer g.mu.Unlock()
	members := make([]*Session[S], 0, len(g.sessions))
	for _, s := range g.sessions {
		members = append(members, s)
	}
	return members
}

// BroadcastOthers applies fn to every session in the group except
// the excluded one. This is the typical pattern when broadcasting from
// inside [HandleFunc]: Handle updates the sender's state directly
// (via the return value) and uses BroadcastOthers to push the change
// to everyone else, avoiding a double-apply on the sender.
//
// Like [Group.Broadcast], this is fire-and-forget and safe to call
// from any goroutine.
func (g *Group[S]) BroadcastOthers(exclude *Session[S], fn func(S) HandleResult[S]) {
	g.mu.Lock()
	targets := make([]*Session[S], 0, len(g.sessions))
	for _, s := range g.sessions {
		if s != exclude {
			targets = append(targets, s)
		}
	}
	g.mu.Unlock()

	for _, s := range targets {
		go s.Update(fn)
	}
}

// Broadcast applies fn to every session in the group via
// [Session.Update]. Each session is updated in its own goroutine so
// a slow render in one session does not block delivery to the rest.
//
// The function fn returns a [HandleResult] so side effects (announce,
// flash, title, URL) can be sent atomically with the state diff. Use
// [Result] to return a bare state when no side effects are needed.
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
func (g *Group[S]) Broadcast(fn func(S) HandleResult[S]) {
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
