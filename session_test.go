package poly

import (
	"io"
	"sync"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// mockTransport records sent messages and replays queued events,
// allowing session event loop tests without a real connection.
type mockTransport struct {
	mu      sync.Mutex
	events  []Event
	patches [][]jit.Patch
	fulls   [][]byte
	closed  bool
}

func (m *mockTransport) SendPatches(patches []jit.Patch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.patches = append(m.patches, patches)
	return nil
}

func (m *mockTransport) SendFull(html []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fulls = append(m.fulls, html)
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

	if len(mt.patches) != 1 {
		t.Fatalf("expected 1 patch send, got %d", len(mt.patches))
	}
	if len(mt.patches[0]) != 1 {
		t.Fatalf("expected 1 patch in first send, got %d", len(mt.patches[0]))
	}
	if mt.patches[0][0].Key != "count" {
		t.Errorf("patch key should be %q, got %q", "count", mt.patches[0][0].Key)
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

	if len(mt.patches) != 0 {
		t.Errorf("expected no patches when state unchanged, got %d", len(mt.patches))
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

	if len(mt.patches) != 0 {
		t.Errorf("expected no patches when Equal returns true, got %d", len(mt.patches))
	}
	if len(mt.fulls) != 0 {
		t.Errorf("expected no full renders when Equal returns true, got %d", len(mt.fulls))
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

	if len(mt.patches) != 3 {
		t.Fatalf("expected 3 patch sends, got %d", len(mt.patches))
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
	}

	tree := sess.render(sess.state)
	differ.Render(tree)

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Structural change — should send a full render, not patches
	if len(mt.fulls) != 1 {
		t.Fatalf("expected 1 full render for structural change, got %d", len(mt.fulls))
	}
	if len(mt.patches) != 0 {
		t.Errorf("expected no patches for structural change, got %d", len(mt.patches))
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
