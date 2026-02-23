package poly

import (
	"io"
	"log/slog"
	"net/url"
	"sync"
	"time"

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
	id           string
	state        S
	render       RenderFunc[S]
	handle       HandleFunc[S]
	handleParams func(S, Params) S
	differ       *jit.Differ
	transport    Transport
	logger       *slog.Logger
	createdAt      time.Time
	lastActivity   time.Time
	disconnectedAt time.Time
	mu             sync.Mutex

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
		s.lastActivity = time.Now()
		s.handleEvent(ev)
		s.mu.Unlock()
	}
}

// Close terminates the session by closing its transport. The event loop
// will exit and the onDisconnect callback will fire. Safe to call from
// any goroutine; safe to call more than once.
func (s *Session[S]) Close() {
	s.transport.Close()
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
	if ev.Type == "navigate" && s.handleParams != nil {
		params := Params{Path: ev.Data["path"]}
		if search := ev.Data["search"]; search != "" {
			params.Query, _ = url.ParseQuery(search)
		}
		s.applyState(s.handleParams(s.state, params))
		return
	}
	s.applyState(s.handle(s.state, ev))
}

// Navigate pushes a URL change to the client. The browser calls
// history.pushState, adding a history entry. Safe to call from
// any goroutine.
func (s *Session[S]) Navigate(rawURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendURL(rawURL, false)
}

// ReplaceURL updates the browser URL without adding a history entry.
// The browser calls history.replaceState. Safe to call from any
// goroutine.
func (s *Session[S]) ReplaceURL(rawURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendURL(rawURL, true)
}

func (s *Session[S]) sendURL(rawURL string, replace bool) {
	update := Update{URL: rawURL, Replace: replace}
	if err := s.transport.SendUpdate(update); err != nil {
		s.logger.Error("send URL error", "session", s.id, "err", err)
	}
}

// applyState sets the new state, diffs the rendered tree, and sends
// an update to the client. Caller must hold s.mu.
func (s *Session[S]) applyState(newState S) {
	if s.equal != nil && s.equal(s.state, newState) {
		return
	}

	s.state = newState
	tree := s.render(s.state)

	patches, change := s.differ.Diff(tree)
	if change != nil {
		// Keys were added, removed, or reordered — morph the root.
		// The client uses idiomorph to morph the entire root, preserving
		// focus, scroll position, and form state.
		html := s.differ.Render(tree)
		s.logger.Warn("structural change, sending root morph",
			"session", s.id,
			"change", change.String(),
			"bytes", len(html),
			"tip", "wrap conditional elements in a keyed container to scope this morph",
		)
		update := Update{
			Morphs: []Morph{{Key: "", HTML: html}},
		}
		if err := s.transport.SendUpdate(update); err != nil {
			s.logger.Error("send update error", "session", s.id, "err", err)
		}
		return
	}

	if len(patches) > 0 {
		update := Update{Patches: patches}
		if err := s.transport.SendUpdate(update); err != nil {
			s.logger.Error("send update error", "session", s.id, "err", err)
		}
	}
}
