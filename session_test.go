package poly

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// patchUpdates returns all updates that contained patches (no morphs).
func patchUpdates(updates []Update) []Update {
	var result []Update
	for _, u := range updates {
		if len(u.Patches) > 0 && len(u.Morphs) == 0 {
			result = append(result, u)
		}
	}
	return result
}

// morphUpdates returns all updates that contained morphs.
func morphUpdates(updates []Update) []Update {
	var result []Update
	for _, u := range updates {
		if len(u.Morphs) > 0 {
			result = append(result, u)
		}
	}
	return result
}

// mockTransport records sent updates and replays queued events,
// allowing session event loop tests without a real connection.
type mockTransport struct {
	mu      sync.Mutex
	events  []Event
	updates []Update
	closed  bool
}

func (m *mockTransport) SendUpdate(update Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, update)
	return nil
}

func (m *mockTransport) ReceiveEvent() (Event, error) {
	m.mu.Lock()
	if len(m.events) == 0 {
		m.mu.Unlock()
		return Event{}, io.EOF
	}
	ev := m.events[0]
	m.events = m.events[1:]
	m.mu.Unlock()
	return ev, nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

type counterState struct {
	Count int
}

func renderCounter(state counterState) node.Node {
	return div.New(
		span.Textf("Count: %d", state.Count).Dynamic("count"),
	)
}

func handleCounter(state counterState, ev Event) counterState {
	switch ev.Action {
	case "increment":
		state.Count++
	case "decrement":
		state.Count--
	}
	return state
}

func TestSessionSendsPatchOnChange(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "increment"},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:        "test",
		state:     counterState{Count: 0},
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
	}

	// Seed the differ with the initial render
	tree := sess.render(sess.state)
	differ.Render(tree)

	// Run the event loop — it processes one event then gets EOF
	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	pUpdates := patchUpdates(mt.updates)
	if len(pUpdates) != 1 {
		t.Fatalf("expected 1 patch update, got %d", len(pUpdates))
	}
	if len(pUpdates[0].Patches) != 1 {
		t.Fatalf("expected 1 patch in first update, got %d", len(pUpdates[0].Patches))
	}
	if pUpdates[0].Patches[0].Key != "count" {
		t.Errorf("patch key should be %q, got %q", "count", pUpdates[0].Patches[0].Key)
	}
	if !mt.closed {
		t.Error("transport should be closed after event loop exits")
	}
}

func TestSessionNoPatchWhenUnchanged(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			// Action that doesn't change state
			{Type: "click", Action: "noop"},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:        "test",
		state:     counterState{Count: 5},
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(patchUpdates(mt.updates)) != 0 {
		t.Errorf("expected no patch updates when state unchanged, got %d", len(patchUpdates(mt.updates)))
	}
}

func TestSessionEqualSkipsDiff(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "noop"},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:        "test",
		state:     counterState{Count: 5},
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
		equal: func(a, b counterState) bool {
			return a.Count == b.Count
		},
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 0 {
		t.Errorf("expected no updates when Equal returns true, got %d", len(mt.updates))
	}
}

func TestSessionMultipleEvents(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "increment"},
			{Type: "click", Action: "increment"},
			{Type: "click", Action: "increment"},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:        "test",
		state:     counterState{Count: 0},
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	pUpdates := patchUpdates(mt.updates)
	if len(pUpdates) != 3 {
		t.Fatalf("expected 3 patch updates, got %d", len(pUpdates))
	}

	// Final state should be Count: 3
	if sess.state.Count != 3 {
		t.Errorf("final state should have Count 3, got %d", sess.state.Count)
	}
}

func TestSessionStructuralChange(t *testing.T) {
	type state struct {
		Count    int
		ShowHelp bool
	}

	render := func(s state) node.Node {
		children := []node.Node{
			span.Textf("Count: %d", s.Count).Dynamic("count"),
		}
		if s.ShowHelp {
			children = append(children,
				span.Text("Help text").Dynamic("help"),
			)
		}
		return div.New(children...)
	}

	handle := func(s state, ev Event) state {
		if ev.Action == "toggle-help" {
			s.ShowHelp = !s.ShowHelp
		}
		return s
	}

	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "toggle-help"},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[state]{
		id:        "test",
		state:     state{Count: 0, ShowHelp: false},
		render:    render,
		handle:    handle,
		differ:    differ,
		transport: mt,
		logger:    slog.Default(),
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Structural change — should send a morph, not patches
	mUpdates := morphUpdates(mt.updates)
	if len(mUpdates) != 1 {
		t.Fatalf("expected 1 morph update for structural change, got %d", len(mUpdates))
	}
	if len(patchUpdates(mt.updates)) != 0 {
		t.Errorf("expected no patch updates for structural change, got %d", len(patchUpdates(mt.updates)))
	}
}

func TestSessionNavigateEvent(t *testing.T) {
	type state struct {
		Page string
	}

	render := func(s state) node.Node {
		return div.New(
			span.Text(s.Page).Dynamic("page"),
		)
	}

	handleParams := func(s state, params Params) state {
		s.Page = params.Path
		return s
	}

	mt := &mockTransport{
		events: []Event{
			{Type: "navigate", Data: map[string]string{"path": "/profile", "search": ""}},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[state]{
		id:           "test",
		state:        state{Page: "/"},
		render:       render,
		handle:       func(s state, ev Event) state { return s },
		handleParams: handleParams,
		differ:       differ,
		transport:    mt,
		logger:       slog.Default(),
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	if sess.state.Page != "/profile" {
		t.Errorf("expected page %q, got %q", "/profile", sess.state.Page)
	}

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) == 0 {
		t.Fatal("expected at least one update after navigation")
	}
}

func TestSessionNavigateEventWithQuery(t *testing.T) {
	type state struct {
		Page string
		Tab  string
	}

	var receivedQuery string

	handleParams := func(s state, params Params) state {
		s.Page = params.Path
		if params.Query != nil {
			receivedQuery = params.Query.Get("tab")
			s.Tab = receivedQuery
		}
		return s
	}

	mt := &mockTransport{
		events: []Event{
			{Type: "navigate", Data: map[string]string{"path": "/settings", "search": "tab=security"}},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[state]{
		id:           "test",
		state:        state{Page: "/"},
		render:       func(s state) node.Node { return div.New(span.Text(s.Page).Dynamic("page")) },
		handle:       func(s state, ev Event) state { return s },
		handleParams: handleParams,
		differ:       differ,
		transport:    mt,
		logger:       slog.Default(),
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	if sess.state.Tab != "security" {
		t.Errorf("expected tab %q, got %q", "security", sess.state.Tab)
	}
}

func TestSessionNavigateEventWithoutHandleParams(t *testing.T) {
	// When handleParams is nil, navigate events fall through to handle
	var receivedAction string

	mt := &mockTransport{
		events: []Event{
			{Type: "navigate", Data: map[string]string{"path": "/about"}},
		},
	}

	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:    "test",
		state: counterState{Count: 0},
		render: renderCounter,
		handle: func(s counterState, ev Event) counterState {
			receivedAction = ev.Type
			return s
		},
		differ:    differ,
		transport: mt,
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	if receivedAction != "navigate" {
		t.Errorf("expected handle to receive navigate event, got %q", receivedAction)
	}
}

func TestSessionNavigateSendsURL(t *testing.T) {
	mt := &mockTransport{
		events: []Event{}, // EOF immediately
	}

	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:        "test",
		state:     counterState{Count: 0},
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
		logger:    slog.Default(),
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	// Call Navigate directly before running the event loop
	sess.Navigate("/new-page")

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].URL != "/new-page" {
		t.Errorf("expected URL %q, got %q", "/new-page", mt.updates[0].URL)
	}
	if mt.updates[0].Replace {
		t.Error("Navigate should use pushState (Replace=false)")
	}
}

func TestSessionReplaceURLSendsReplace(t *testing.T) {
	mt := &mockTransport{
		events: []Event{}, // EOF immediately
	}

	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:        "test",
		state:     counterState{Count: 0},
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
		logger:    slog.Default(),
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.ReplaceURL("/current")

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].URL != "/current" {
		t.Errorf("expected URL %q, got %q", "/current", mt.updates[0].URL)
	}
	if !mt.updates[0].Replace {
		t.Error("ReplaceURL should use replaceState (Replace=true)")
	}
}

func TestSessionOnDisconnect(t *testing.T) {
	mt := &mockTransport{
		events: []Event{}, // EOF immediately
	}

	called := false
	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:           "test",
		state:        counterState{Count: 0},
		render:       renderCounter,
		handle:       handleCounter,
		differ:       differ,
		transport:    mt,
		onDisconnect: func() { called = true },
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	if !called {
		t.Error("onDisconnect should be called when session ends")
	}
}
