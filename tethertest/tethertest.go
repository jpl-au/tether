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
	"context"
	"maps"
	"net/url"
	"strings"

	"github.com/jpl-au/fluent/node"
	tether "github.com/jpl-au/tether"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/push"
)

// HandleFunc is the handler signature for tethertest. It is identical
// to [tether.HandleFunc] — both take [tether.Session] — so handler
// functions can be shared across live mode, page mode, and tests
// without changing their signature.
type HandleFunc[S any] func(session tether.Session, state S, event tether.Event) S

// Middleware wraps a [HandleFunc] to add cross-cutting behaviour.
// Identical to [tether.Middleware] so middleware can be shared across
// live mode and tests.
type Middleware[S any] func(next HandleFunc[S]) HandleFunc[S]

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
	Middleware []Middleware[S]

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

	// Last response fields.
	last response
}

// response holds the captured result of the most recent Send call.
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

// testSession implements [tether.Session] for the test harness. Every
// side effect that a Handle function can produce (toasts, navigation,
// title changes, signals, flash messages, announcements) is captured
// into local fields rather than sent to a client. The harness exposes
// these via assertion helpers — HasToast, URL, Title, Signals, etc. —
// so tests verify behaviour without a transport layer.
//
// Each call to [Harness.Send], [Harness.SendEvent], or [Harness.Navigate]
// creates a fresh testSession, so assertions always reflect the most
// recent event cycle.
type testSession struct {
	toast    string
	url      string
	replace  bool
	title    string
	announce string
	flash    map[string]string
	signals  map[string]any
}

// ID returns a fixed identifier. All harness events appear to come
// from the same session, which simplifies test assertions and avoids
// non-deterministic IDs in snapshot comparisons.
func (s *testSession) ID() string { return "tethertest" }

// Context returns a detached background context. The test harness
// does not model session lifecycle cancellation — tests run
// synchronously and do not need context-driven teardown.
func (s *testSession) Context() context.Context { return context.Background() }

// Go spawns a goroutine against a background context. In tests this
// runs truly concurrently — test code that calls Go should use
// synchronisation if it needs to observe the goroutine's effects.
func (s *testSession) Go(fn func(context.Context)) { go fn(context.Background()) }

// Toast captures a toast message. Assert with [Harness.HasToast] or
// read directly with [Harness.Toast].
func (s *testSession) Toast(text string) { s.toast = text }

// SetTitle captures a document title change. Read with [Harness.Title].
func (s *testSession) SetTitle(title string) { s.title = title }

// Announce captures an accessibility announcement. Assert with
// [Harness.HasAnnounce] or read with [Harness.Announce].
func (s *testSession) Announce(text string) { s.announce = text }

// Navigate captures a client-side navigation with a new history entry.
// Read the target URL with [Harness.URL].
func (s *testSession) Navigate(rawURL string) { s.url = rawURL; s.replace = false }

// ReplaceURL captures a history replacement (no new entry). Read the
// target URL with [Harness.URL]; distinguish from Navigate with
// [Harness.URLWasReplaced].
func (s *testSession) ReplaceURL(rawURL string) { s.url = rawURL; s.replace = true }

// Signal captures a single signal update. Assert with [Harness.HasSignal]
// or read the full map with [Harness.Signals].
func (s *testSession) Signal(key string, v any) { s.ensureSignals(); s.signals[key] = v }

// Signals captures a batch signal update, merging into any previously
// set signals from the same event cycle.
func (s *testSession) Signals(m map[string]any) {
	s.ensureSignals()
	maps.Copy(s.signals, m)
}

// Flash captures a targeted flash message keyed by CSS selector.
// Assert with [Harness.HasFlash] or read the full map with
// [Harness.Flash].
func (s *testSession) Flash(sel, text string) { s.ensureFlash(); s.flash[sel] = text }

// Push is a no-op in the test harness. Push notifications require a
// browser subscription that does not exist in unit tests.
func (s *testSession) Push(push.Notification) error { return nil }

// Close is a no-op in the test harness. There is no transport to shut
// down — the harness manages session lifecycle explicitly via
// [Harness.Connect] and [Harness.Disconnect].
func (s *testSession) Close() {}

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

// chainMiddleware applies middleware outermost-first, mirroring the
// order used by [tether.Config].Middleware.
func chainMiddleware[S any](h HandleFunc[S], mw []Middleware[S]) HandleFunc[S] {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// New creates a test harness. The harness invokes Handle directly —
// no HTTP server, no JSON round-trip, no goroutines.
func New[S any](cfg Config[S]) *Harness[S] {
	handle := HandleFunc[S](cfg.Handle)
	if len(cfg.Middleware) > 0 {
		handle = chainMiddleware(handle, cfg.Middleware)
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
	ts := &testSession{}
	h.state = h.dispatch(ts, ev)

	h.last = response{
		toast:    ts.toast,
		url:      ts.url,
		replace:  ts.replace,
		title:    ts.title,
		flash:    ts.flash,
		signals:  ts.signals,
		announce: ts.announce,
	}

	if h.render != nil {
		h.last.html = string(h.renderHTML(h.state))
	}
}

// dispatch routes the event to the correct handler: navigate events go
// to OnNavigate, component-prefixed events go to RouteMount, and
// everything else goes to Handle.
func (h *Harness[S]) dispatch(ts *testSession, ev tether.Event) S {
	if ev.Type == event.Navigate && h.onNavigate != nil {
		params := tether.Params{
			Path:  ev.Data["path"],
			Query: parseQuery(ev.Data["search"]),
		}
		return h.onNavigate(ts, h.state, params)
	}
	if newState, ok := tether.RouteMount(h.mounts, ts, h.state, ev); ok {
		return newState
	}
	return h.handle(ts, h.state, ev)
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
// Returns nil if no signals were pushed. Values retain their original
// Go types — no JSON round-tripping.
func (h *Harness[S]) Signals() map[string]any {
	return h.last.signals
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
	v, ok := h.last.signals[key]
	return ok && v == value
}

// HasAnnounce reports whether the most recent Send call triggered an
// accessibility announcement matching the given text.
func (h *Harness[S]) HasAnnounce(text string) bool {
	return h.last.announce == text
}

// HasFlash reports whether the most recent Send call triggered a flash
// message matching the given selector and text.
func (h *Harness[S]) HasFlash(selector, text string) bool {
	return h.last.flash != nil && h.last.flash[selector] == text
}

// Connect triggers the OnConnect callback, simulating a client
// connecting to the server. Panics if OnConnect was not configured.
func (h *Harness[S]) Connect() {
	if h.onConnect == nil {
		panic("tethertest: Connect called but OnConnect is not configured")
	}
	h.onConnect(&testSession{})
}

// Disconnect triggers the OnDisconnect callback, simulating a client
// disconnecting from the server. Panics if OnDisconnect was not configured.
func (h *Harness[S]) Disconnect() {
	if h.onDisconnect == nil {
		panic("tethertest: Disconnect called but OnDisconnect is not configured")
	}
	h.onDisconnect(&testSession{})
}

// URLWasReplaced reports whether the most recent URL change used
// ReplaceURL rather than Navigate. Returns false if no URL was set.
func (h *Harness[S]) URLWasReplaced() bool {
	return h.last.replace
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
