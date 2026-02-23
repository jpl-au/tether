package poly

import (
	"io"
	"log/slog"
	"sync"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
)

// RenderFunc builds a node tree from the current state.
// Called on initial render and after each event.
type RenderFunc[S any] func(state S) node.Node

// HandleFunc processes an event and returns updated state.
// The original state is not modified — return a new value.
type HandleFunc[S any] func(state S, event Event) S

// Session represents a single connected client. Each browser tab that
// connects gets its own Session with its own state and diff engine.
// Sessions are not shared across connections.
type Session[S any] struct {
	id        string
	state     S
	render    RenderFunc[S]
	handle    HandleFunc[S]
	differ    *jit.Differ
	transport Transport
	logger    *slog.Logger
	mu        sync.Mutex

	// Optional callbacks from Config
	onDisconnect func()
	equal        func(a, b S) bool
}

// ID returns the session identifier.
func (s *Session[S]) ID() string {
	return s.id
}

// State returns a copy of the current session state.
func (s *Session[S]) State() S {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// run is the event loop. It blocks until the transport returns io.EOF
// (client disconnected) or an unrecoverable error occurs.
func (s *Session[S]) run() {
	defer func() {
		s.transport.Close()
		if s.onDisconnect != nil {
			s.onDisconnect()
		}
	}()

	for {
		ev, err := s.transport.ReceiveEvent()
		if err != nil {
			if err == io.EOF {
				return
			}
			s.logger.Error("receive error", "session", s.id, "err", err)
			return
		}

		s.mu.Lock()
		s.handleEvent(ev)
		s.mu.Unlock()
	}
}

// Update applies a state change from outside the event loop and pushes
// the resulting diff to the client. Safe to call from any goroutine.
// Use this for server-initiated updates like timers, database changes,
// or broadcasts from other sessions.
func (s *Session[S]) Update(fn func(S) S) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.applyState(fn(s.state))
}

// handleEvent processes a single event. Caller must hold s.mu.
func (s *Session[S]) handleEvent(ev Event) {
	s.applyState(s.handle(s.state, ev))
}

// applyState sets the new state, diffs the rendered tree, and sends
// patches to the client. Caller must hold s.mu.
func (s *Session[S]) applyState(newState S) {
	if s.equal != nil && s.equal(s.state, newState) {
		return
	}

	s.state = newState
	tree := s.render(s.state)

	patches, structural := s.differ.Diff(tree)
	if structural {
		// Keys were added or removed — full re-render. The client
		// uses idiomorph to morph the entire root, preserving DOM state.
		html := s.differ.Render(tree)
		if err := s.transport.SendFull(html); err != nil {
			s.logger.Error("send full error", "session", s.id, "err", err)
		}
		return
	}

	if len(patches) > 0 {
		if err := s.transport.SendPatches(patches); err != nil {
			s.logger.Error("send patches error", "session", s.id, "err", err)
		}
	}
}
