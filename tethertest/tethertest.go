// Package tethertest provides a test harness for tether Handle functions.
// It invokes the handler directly — no HTTP server, no JSON serialisation,
// no transport plumbing — so tests see the exact types the handler pushed.
//
//	func TestIncrement(t *testing.T) {
//	    h := tethertest.New(tethertest.Config[State]{
//	        Handle: handle,
//	    })
//
//	    h.Send("increment")
//
//	    if h.State().Count != 1 {
//	        t.Errorf("got %d, want 1", h.State().Count)
//	    }
//	}
package tethertest

import (
	"net/url"
	"strings"

	"github.com/jpl-au/fluent/node"
	tether "github.com/jpl-au/tether"
	"github.com/jpl-au/tether/event"
)

// Config configures the test harness.
type Config[S any] struct {
	// State is the initial state for each test interaction.
	State S

	// Render builds a node tree from the current state. Optional —
	// only required when calling [Harness.HTML], [Harness.Render],
	// or [Harness.RenderNode].
	Render tether.RenderFunc[S]

	// Handle processes a client event and returns the new state.
	Handle func(session tether.Session, state S, event tether.Event) S

	// Middleware wraps the Handle function with cross-cutting
	// behaviour. Applied outermost-first: the first entry in the
	// slice is the outermost layer of the chain. Optional.
	Middleware []tether.Middleware[S]

	// OnNavigate processes URL parameters. Optional.
	OnNavigate func(session tether.Session, state S, params tether.Params) S

	// OnConnect is called when [Harness.Connect] is called. Use this
	// to test session registration logic (e.g. joining a [tether.Group]
	// or starting background tasks). Optional.
	OnConnect func(session tether.Session)

	// OnDisconnect is called when [Harness.Disconnect] is called. Use
	// this to test cleanup logic (e.g. removing from a [tether.Group]
	// or decrementing counters). Optional.
	OnDisconnect func(session tether.Session)

	// Components declares component mounts for automatic event routing.
	// Events matching a mount's prefix are dispatched to the component
	// before Handle runs — mirroring [tether.Config].Components.
	// Optional.
	Components []tether.ComponentMount[S]

	// Layout wraps the rendered content for [Harness.Render] and
	// [Harness.HTML], mirroring [tether.Config].Layout. Optional —
	// when absent, only the content node is rendered.
	Layout func(S, node.Node) node.Node
}

// Harness drives a tether handler for testing. Create one with [New],
// send events with [Harness.Send] or [Harness.SendEvent], and inspect
// the result with the accessor methods.
type Harness[S any] struct {
	state      S
	render     tether.RenderFunc[S]
	handle     func(tether.Session, S, tether.Event) S
	onNavigate func(tether.Session, S, tether.Params) S
	layout     func(S, node.Node) node.Node

	// Lifecycle callbacks stored from Config.
	onConnect    func(tether.Session)
	onDisconnect func(tether.Session)

	// Component mounts for automatic event routing.
	mounts []tether.ComponentMount[S]

	// Last captured effects and rendered HTML.
	last     tether.Effects
	lastHTML string
}

// New creates a test harness. The harness invokes Handle directly —
// no HTTP server, no JSON round-trip, no goroutines.
func New[S any](cfg Config[S]) *Harness[S] {
	handle := cfg.Handle
	if len(cfg.Middleware) > 0 {
		handle = tether.Chain(handle, cfg.Middleware)
	}
	return &Harness[S]{
		state:        cfg.State,
		render:       cfg.Render,
		handle:       handle,
		onNavigate:   cfg.OnNavigate,
		onConnect:    cfg.OnConnect,
		onDisconnect: cfg.OnDisconnect,
		mounts:       cfg.Components,
		layout:       cfg.Layout,
	}
}

// Send fires a click event with the given action name. This is the
// most common case — use [Harness.SendEvent] for other event types.
func (h *Harness[S]) Send(action string) {
	h.SendEvent(tether.Event{
		Type:   event.Click,
		Action: action,
		Data:   map[string]string{},
	})
}

// SendInput fires an input event with the given action and value.
func (h *Harness[S]) SendInput(action, value string) {
	h.SendEvent(tether.Event{
		Type:   event.Input,
		Action: action,
		Data:   map[string]string{"value": value},
	})
}

// SendSubmit fires a submit event with the given action and form data.
func (h *Harness[S]) SendSubmit(action string, data map[string]string) {
	h.SendEvent(tether.Event{
		Type:   event.Submit,
		Action: action,
		Data:   data,
	})
}

// SendEvent fires an arbitrary event and captures the response. After
// this call, [Harness.State], [Harness.HTML], [Harness.Toast], etc.
// reflect the result of handling this event.
func (h *Harness[S]) SendEvent(ev tether.Event) {
	cs := &tether.CaptureSession{SessionID: "tethertest"}
	h.state = h.dispatch(cs, ev)
	h.last = cs.Effects
	h.lastHTML = ""
	if h.render != nil {
		h.lastHTML = string(h.renderHTML(h.state))
	}
}

// dispatch routes the event to the correct handler: navigate events go
// to OnNavigate, component-prefixed events go to RouteMount, and
// everything else goes to Handle.
func (h *Harness[S]) dispatch(cs *tether.CaptureSession, ev tether.Event) S {
	if ev.Type == event.Navigate && h.onNavigate != nil {
		params := tether.Params{
			Path:  ev.Data["path"],
			Query: parseQuery(ev.Data["search"]),
		}
		return h.onNavigate(cs, h.state, params)
	}
	if newState, ok := tether.RouteMount(h.mounts, cs, h.state, ev); ok {
		return newState
	}
	return h.handle(cs, h.state, ev)
}

// renderHTML renders the current state to HTML bytes, applying Layout
// if configured.
func (h *Harness[S]) renderHTML(s S) []byte {
	content := h.render(s)
	if h.layout != nil {
		return h.layout(s, content).Render()
	}
	return content.Render()
}

// State returns the current accumulated state. Each Send call applies
// the Handle function, so State reflects all events sent so far.
func (h *Harness[S]) State() S {
	return h.state
}

// HTML returns the rendered HTML from the most recent Send call.
// Returns an empty string if Render was not configured.
func (h *Harness[S]) HTML() string {
	return h.lastHTML
}

// Toast returns the toast message from the most recent Send call.
// Returns an empty string if no toast was triggered.
func (h *Harness[S]) Toast() string {
	return h.last.Toast
}

// HasToast reports whether the most recent Send call triggered a
// toast matching the given text.
func (h *Harness[S]) HasToast(text string) bool {
	return h.last.Toast == text
}

// URL returns the navigation URL from the most recent Send call.
// Returns an empty string if no navigation was triggered.
func (h *Harness[S]) URL() string {
	return h.last.URL
}

// Title returns the title from the most recent Send call. Returns an
// empty string if no title change was triggered.
func (h *Harness[S]) Title() string {
	return h.last.Title
}

// Announce returns the accessibility announcement from the most
// recent Send call. Returns an empty string if none was triggered.
func (h *Harness[S]) Announce() string {
	return h.last.Announce
}

// Flash returns the flash messages from the most recent Send call.
// Returns nil if no flash messages were triggered.
func (h *Harness[S]) Flash() map[string]string {
	return h.last.Flash
}

// Signals returns the signal values from the most recent Send call.
// Returns nil if no signals were pushed. Values retain their original
// Go types — no JSON round-tripping.
func (h *Harness[S]) Signals() map[string]any {
	return h.last.Signals
}

// Render returns the rendered HTML for the current state. When Layout
// is configured, the content is wrapped in the layout. Returns an
// empty string if Render was not configured.
func (h *Harness[S]) Render() string {
	if h.render == nil {
		return ""
	}
	return string(h.renderHTML(h.state))
}

// RenderNode returns the node tree for the current state. Useful for
// inspecting the tree structure directly. Panics if Render was not
// configured.
func (h *Harness[S]) RenderNode() node.Node {
	return h.render(h.state)
}

// Navigate sends a navigate event with the given path. Query
// parameters in the path are parsed and delivered to OnNavigate.
func (h *Harness[S]) Navigate(path string) {
	data := map[string]string{"path": path}
	if i := strings.Index(path, "?"); i >= 0 {
		data["path"] = path[:i]
		data["search"] = path[i:]
	}
	h.SendEvent(tether.Event{
		Type:   event.Navigate,
		Action: "",
		Data:   data,
	})
}

// HasSignal reports whether the most recent Send call pushed a signal
// matching the given key and value. Values are compared with == so
// the expected type must match exactly (e.g. int, not float64).
func (h *Harness[S]) HasSignal(key string, value any) bool {
	v, ok := h.last.Signals[key]
	return ok && v == value
}

// HasAnnounce reports whether the most recent Send call triggered an
// accessibility announcement matching the given text.
func (h *Harness[S]) HasAnnounce(text string) bool {
	return h.last.Announce == text
}

// HasFlash reports whether the most recent Send call triggered a flash
// message matching the given selector and text.
func (h *Harness[S]) HasFlash(selector, text string) bool {
	return h.last.Flash != nil && h.last.Flash[selector] == text
}

// Connect triggers the OnConnect callback, simulating a client
// connecting to the server. Panics if OnConnect was not configured.
func (h *Harness[S]) Connect() {
	if h.onConnect == nil {
		panic("tethertest: Connect called but OnConnect is not configured")
	}
	h.onConnect(&tether.CaptureSession{SessionID: "tethertest"})
}

// Disconnect triggers the OnDisconnect callback, simulating a client
// disconnecting from the server. Panics if OnDisconnect was not configured.
func (h *Harness[S]) Disconnect() {
	if h.onDisconnect == nil {
		panic("tethertest: Disconnect called but OnDisconnect is not configured")
	}
	h.onDisconnect(&tether.CaptureSession{SessionID: "tethertest"})
}

// URLWasReplaced reports whether the most recent URL change used
// ReplaceURL rather than Navigate. Returns false if no URL was set.
func (h *Harness[S]) URLWasReplaced() bool {
	return h.last.Replace
}

// parseQuery extracts query parameters from a search string (e.g.
// "?id=42"). Returns nil for empty input. Panics on malformed input
// so test failures surface immediately.
func parseQuery(search string) url.Values {
	search = strings.TrimPrefix(search, "?")
	if search == "" {
		return nil
	}
	v, err := url.ParseQuery(search)
	if err != nil {
		panic("tethertest: malformed query string: " + err.Error())
	}
	return v
}
