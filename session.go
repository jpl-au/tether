package poly

import (
	"errors"
	"io"
	"log/slog"
	"net/url"
	"sync"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
)

// StructuralChange describes a diff result where the render tree's
// Dynamic key set changed — keys were added, removed, or reordered.
// This forces a full root morph instead of targeted patches. The
// fields mirror [jit.StructuralChange] so callers don't need to import
// the diff engine package.
type StructuralChange struct {
	Added     []string // keys present in the new tree but not the old
	Removed   []string // keys present in the old tree but not the new
	Reordered bool     // same keys, different order
	Bytes     int      // size of the re-rendered HTML sent as a root morph
}

// RenderFunc builds a Fluent node tree from the current state. It is
// called on initial page render, after each client event, and after
// each call to [Session.Update]. The function must be pure — given the
// same state it must always produce the same tree, because the diff
// engine compares consecutive renders to compute patches.
type RenderFunc[S any] func(state S) node.Node

// HandleFunc processes a client event and returns updated state. The
// function should treat the input state as immutable — return a new
// value with the desired changes. Returning the original state
// unchanged is valid and will produce no diff (especially when an
// Equal function is configured).
type HandleFunc[S any] func(state S, event Event) S

// Session represents a single connected client. Each browser tab gets
// its own Session with independent state and a dedicated diff engine.
// Sessions are never shared across connections — concurrent access to
// the same Session is serialised by an internal mutex.
//
// The exported methods (Update, Navigate, ReplaceURL, SetTitle, Close)
// are safe to call from any goroutine, making it possible to push
// server-initiated updates from background goroutines, timers, or
// database change listeners.
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
	onDisconnect       func()
	equal              func(a, b S) bool
	onStructuralChange func(*Session[S], StructuralChange)
}

// ID returns the unique session identifier. This is a cryptographically
// random string generated when the session is created. It can be used
// for logging, metrics, or as a key in external storage.
func (s *Session[S]) ID() string {
	return s.id
}

// State returns the current session state. The value is read under the
// session lock, so it reflects the state as of the most recently
// completed event or Update call. Because Go copies values on return,
// the caller gets a snapshot — mutations to it do not affect the
// session. Use [Session.Update] to apply state changes.
func (s *Session[S]) State() S {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// run is the session's event loop. It blocks the calling goroutine,
// reading events from the transport and processing them one at a time.
// The loop exits when the transport returns io.EOF (clean disconnect)
// or any other error (broken connection). On exit it closes the
// transport and fires the onDisconnect callback, which moves the
// session to the disconnected pool or removes it entirely.
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
			if errors.Is(err, io.EOF) {
				return
			}
			s.logger.Error("receive error", "session", s.id, "err", err)
			return
		}

		s.mu.Lock()
		s.lastActivity = time.Now()
		s.safeHandleEvent(ev)
		s.mu.Unlock()
	}
}

// Close terminates the session by closing its transport. The event loop
// will exit and the onDisconnect callback will fire. Safe to call from
// any goroutine; safe to call more than once.
func (s *Session[S]) Close() {
	// Read transport under the lock because reattach writes it
	// concurrently when a disconnected session reconnects.
	s.mu.Lock()
	t := s.transport
	s.mu.Unlock()
	t.Close()
}

// Update applies a state change from outside the normal event loop and
// pushes the resulting diff to the client. This is the primary way to
// push server-initiated updates — call it from timers, database change
// listeners, message queue consumers, or [Group.Broadcast].
//
// The function fn receives the current state and returns the new state.
// It runs under the session lock, so it is serialised with client
// events — there is no risk of concurrent state mutation. Panics in fn
// or in the subsequent render pass are recovered and logged rather than
// crashing the calling goroutine.
//
// Safe to call from any goroutine.
func (s *Session[S]) Update(fn func(S) S) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in Update callback",
				"session", s.id,
				"panic", r,
			)
		}
	}()

	// Server-initiated updates count as activity so that sessions
	// receiving only server pushes are not reaped as idle.
	s.lastActivity = time.Now()
	s.applyState(fn(s.state), "")
}

// safeHandleEvent wraps handleEvent with panic recovery so that a
// bug in Handle or Render does not kill the session. The panic is
// logged and the event is dropped — the session continues processing
// subsequent events.
func (s *Session[S]) safeHandleEvent(ev Event) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in event handler",
				"session", s.id,
				"action", ev.Action,
				"panic", r,
			)
		}
	}()
	s.handleEvent(ev)
}

// handleEvent processes a single event. Navigate events are routed to
// handleParams (when configured) so URL changes can update state without
// going through the general Handle function. All other events go through
// Handle. The eventID from the client is threaded through to applyState
// so the response can be correlated with the triggering event.
//
// Caller must hold s.mu.
func (s *Session[S]) handleEvent(ev Event) {
	if ev.Type == "navigate" && s.handleParams != nil {
		params := Params{Path: ev.Data["path"]}
		if search := ev.Data["search"]; search != "" {
			params.Query, _ = url.ParseQuery(search)
		}
		s.applyState(s.handleParams(s.state, params), ev.EventID)
		return
	}
	s.applyState(s.handle(s.state, ev), ev.EventID)
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

// SetTitle updates the browser's document title. Safe to call from
// any goroutine. Can be combined with Navigate or sent standalone.
func (s *Session[S]) SetTitle(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update := Update{Title: title}
	if err := s.transport.SendUpdate(update); err != nil {
		s.logger.Error("send title error", "session", s.id, "err", err)
	}
}

func (s *Session[S]) sendURL(rawURL string, replace bool) {
	update := Update{URL: rawURL, Replace: replace}
	if err := s.transport.SendUpdate(update); err != nil {
		s.logger.Error("send URL error", "session", s.id, "err", err)
	}
}

// applyState is the core render-diff-send pipeline. It stores the new
// state, renders the tree, diffs against the previous render, and sends
// only the changed fragments to the client. When the diff engine detects
// a structural change (nodes added or removed outside a Dynamic key), it
// falls back to a full root morph and logs a warning to help the
// developer scope the change with a keyed container.
//
// The eventID is echoed back in the update so the client JS can
// correlate the response with the event that triggered it. This is used
// to restore loading states (e.g. re-enabling a button) on the specific
// element that initiated the action.
//
// Caller must hold s.mu.
func (s *Session[S]) applyState(newState S, eventID string) {
	if s.equal != nil && s.equal(s.state, newState) {
		// Still echo the eventID so the client can restore any loading
		// state (e.g. a disabled button) even when nothing changed.
		if eventID != "" {
			if err := s.transport.SendUpdate(Update{EventID: eventID}); err != nil {
				s.logger.Error("send update error", "session", s.id, "err", err)
			}
		}
		return
	}

	s.state = newState
	tree := s.render(s.state)

	patches, change := s.differ.Diff(tree)
	if change != nil {
		html := s.differ.Render(tree)
		s.logger.Warn("structural change, sending root morph",
			"session", s.id,
			"change", change.String(),
			"bytes", len(html),
			"tip", "wrap conditional elements in a keyed container to scope this morph",
		)

		if s.onStructuralChange != nil {
			s.onStructuralChange(s, StructuralChange{
				Added:     change.Added,
				Removed:   change.Removed,
				Reordered: change.Reordered,
				Bytes:     len(html),
			})
		}

		update := Update{
			Morphs:  []Morph{{Key: "", HTML: html}},
			EventID: eventID,
		}
		if err := s.transport.SendUpdate(update); err != nil {
			s.logger.Error("send update error", "session", s.id, "err", err)
		}
		return
	}

	if len(patches) > 0 {
		update := Update{Patches: patches, EventID: eventID}
		if err := s.transport.SendUpdate(update); err != nil {
			s.logger.Error("send update error", "session", s.id, "err", err)
		}
		return
	}

	// No patches and no structural change — the rendered tree is
	// identical. Still echo the eventID so the client can restore
	// any loading state (e.g. a disabled button).
	if eventID != "" {
		if err := s.transport.SendUpdate(Update{EventID: eventID}); err != nil {
			s.logger.Error("send update error", "session", s.id, "err", err)
		}
	}
}
