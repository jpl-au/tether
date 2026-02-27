// Package polytest provides a test harness for poly Handle functions.
// It wraps [poly.Page] internally so developers can test event
// handling without setting up channels, transports, or goroutines.
//
//	func TestIncrement(t *testing.T) {
//	    h := polytest.New(polytest.Config[State]{
//	        State:  State{Count: 0},
//	        Render: render,
//	        Handle: handle,
//	    })
//
//	    h.Send("increment")
//
//	    if h.State().Count != 1 {
//	        t.Errorf("got %d, want 1", h.State().Count)
//	    }
//	}
package polytest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	poly "github.com/jpl-au/fluent-poly"
	"github.com/jpl-au/fluent-poly/event"
	"github.com/jpl-au/fluent-poly/push"
	"github.com/jpl-au/fluent/node"
)

// Config configures the test harness.
type Config[S any] struct {
	// State is the initial state for each test interaction.
	State S

	// Render builds a node tree from the current state.
	Render poly.RenderFunc[S]

	// Handle processes a client event and returns the new state.
	Handle func(session poly.PreSession, state S, event poly.Event) S

	// OnNavigate processes URL parameters. Optional.
	OnNavigate func(session poly.PreSession, state S, params poly.Params) S
}

// Harness drives a poly page handler for testing. Create one with
// [New], send events with [Harness.Send] or [Harness.SendEvent], and
// inspect the result with the accessor methods.
type Harness[S any] struct {
	state      S
	render     poly.RenderFunc[S]
	handle     func(poly.PreSession, S, poly.Event) S
	onNavigate func(poly.PreSession, S, poly.Params) S
	handler    http.Handler

	// Last response fields.
	last response
}

// response holds the decoded result of the most recent Send call.
type response struct {
	html     string
	toast    string
	url      string
	replace  bool
	title    string
	flash    map[string]string
	signals  map[string]any
	announce string
}

// wireMessage mirrors the JSON update format from the poly wire
// protocol. Only the fields needed for test assertions are included.
type wireMessage struct {
	Morphs   []wireEntry       `json:"morphs,omitempty"`
	URL      string            `json:"url,omitempty"`
	Replace  bool              `json:"replace,omitempty"`
	Title    string            `json:"title,omitempty"`
	Flash    map[string]string `json:"flash,omitempty"`
	Signals  map[string]any    `json:"signals,omitempty"`
	Announce string            `json:"announce,omitempty"`
	Toast    string            `json:"toast,omitempty"`
}

type wireEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// testSession implements [poly.PreSession] for local state tracking.
// It captures side effects so the harness can report them.
type testSession struct {
	toast    string
	url      string
	replace  bool
	title    string
	announce string
	flash    map[string]string
	signals  map[string]any
}

func (s *testSession) ID() string               { return "polytest" }
func (s *testSession) Toast(text string)        { s.toast = text }
func (s *testSession) SetTitle(title string)    { s.title = title }
func (s *testSession) Announce(text string)     { s.announce = text }
func (s *testSession) Navigate(rawURL string)   { s.url = rawURL; s.replace = false }
func (s *testSession) ReplaceURL(rawURL string) { s.url = rawURL; s.replace = true }
func (s *testSession) Signal(key string, v any) { s.ensureSignals(); s.signals[key] = v }
func (s *testSession) Signals(m map[string]any) {
	s.ensureSignals()
	for k, v := range m {
		s.signals[k] = v
	}
}
func (s *testSession) Flash(sel, text string)       { s.ensureFlash(); s.flash[sel] = text }
func (s *testSession) Push(push.Notification) error { return nil }

func (s *testSession) ensureSignals() {
	if s.signals == nil {
		s.signals = make(map[string]any)
	}
}

func (s *testSession) ensureFlash() {
	if s.flash == nil {
		s.flash = make(map[string]string)
	}
}

// New creates a test harness. The harness uses [poly.Page] internally
// so each Send call is a stateless HTTP round-trip — no goroutines,
// no channels, no transport plumbing.
func New[S any](cfg Config[S]) *Harness[S] {
	h := &Harness[S]{
		state:      cfg.State,
		render:     cfg.Render,
		handle:     cfg.Handle,
		onNavigate: cfg.OnNavigate,
	}
	h.rebuildHandler()
	return h
}

// rebuildHandler constructs a fresh Page handler from the current state.
func (h *Harness[S]) rebuildHandler() {
	state := h.state
	h.handler = poly.Page(poly.PageConfig[S]{
		State:      func(r *http.Request) S { return state },
		Render:     h.render,
		Handle:     h.handle,
		OnNavigate: h.onNavigate,
	})
}

// Send fires a click event with the given action name. This is the
// most common case — use [Harness.SendEvent] for other event types.
func (h *Harness[S]) Send(action string) {
	h.SendEvent(poly.Event{
		Type:   event.Click,
		Action: action,
		Data:   map[string]string{},
	})
}

// SendInput fires an input event with the given action and value.
func (h *Harness[S]) SendInput(action, value string) {
	h.SendEvent(poly.Event{
		Type:   event.Input,
		Action: action,
		Data:   map[string]string{"value": value},
	})
}

// SendSubmit fires a submit event with the given action and form data.
func (h *Harness[S]) SendSubmit(action string, data map[string]string) {
	h.SendEvent(poly.Event{
		Type:   event.Submit,
		Action: action,
		Data:   data,
	})
}

// SendEvent fires an arbitrary event and captures the response. After
// this call, [Harness.State], [Harness.HTML], [Harness.Toast], etc.
// reflect the result of handling this event.
func (h *Harness[S]) SendEvent(ev poly.Event) {
	// Run through the Page handler to get the wire response (HTML + effects).
	body, _ := json.Marshal(ev)
	req := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)

	var msg wireMessage
	json.Unmarshal(w.Body.Bytes(), &msg)

	html := ""
	if len(msg.Morphs) > 0 {
		html = msg.Morphs[0].HTML
	}

	h.last = response{
		html:     html,
		toast:    msg.Toast,
		url:      msg.URL,
		replace:  msg.Replace,
		title:    msg.Title,
		flash:    msg.Flash,
		signals:  msg.Signals,
		announce: msg.Announce,
	}

	// Re-derive state locally using a testSession that captures effects.
	// This allows subsequent Send calls to see accumulated state changes.
	ts := &testSession{}
	h.state = h.handle(ts, h.state, ev)

	// Rebuild the handler with the updated state.
	h.rebuildHandler()
}

// State returns the current accumulated state. Each Send call applies
// the Handle function, so State reflects all events sent so far.
func (h *Harness[S]) State() S {
	return h.state
}

// HTML returns the rendered HTML from the most recent Send call.
func (h *Harness[S]) HTML() string {
	return h.last.html
}

// Toast returns the toast message from the most recent Send call.
// Returns an empty string if no toast was triggered.
func (h *Harness[S]) Toast() string {
	return h.last.toast
}

// HasToast reports whether the most recent Send call triggered a
// toast matching the given text.
func (h *Harness[S]) HasToast(text string) bool {
	return h.last.toast == text
}

// URL returns the navigation URL from the most recent Send call.
// Returns an empty string if no navigation was triggered.
func (h *Harness[S]) URL() string {
	return h.last.url
}

// Title returns the title from the most recent Send call. Returns an
// empty string if no title change was triggered.
func (h *Harness[S]) Title() string {
	return h.last.title
}

// Announce returns the accessibility announcement from the most
// recent Send call. Returns an empty string if none was triggered.
func (h *Harness[S]) Announce() string {
	return h.last.announce
}

// Flash returns the flash messages from the most recent Send call.
// Returns nil if no flash messages were triggered.
func (h *Harness[S]) Flash() map[string]string {
	return h.last.flash
}

// Signals returns the signal values from the most recent Send call.
// Returns nil if no signals were pushed.
func (h *Harness[S]) Signals() map[string]any {
	return h.last.signals
}

// Render performs a GET request and returns the full rendered HTML.
// This is useful for verifying the initial page render without
// sending any events.
func (h *Harness[S]) Render() string {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	return w.Body.String()
}

// RenderNode returns the node tree for the current state without
// going through the HTTP layer. Useful for inspecting the tree
// structure directly.
func (h *Harness[S]) RenderNode() node.Node {
	return h.render(h.state)
}
