package poly

import (
	"log/slog"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func TestSessionSendsPatchOnChange(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "increment"},
		},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
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
			{Type: "click", Action: "noop"},
		},
	}

	sess := newTestSession(counterState{Count: 5}, mt)
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

	sess := newTestSession(counterState{Count: 5}, mt)
	sess.equal = func(a, b counterState) bool {
		return a.Count == b.Count
	}

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

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	pUpdates := patchUpdates(mt.updates)
	if len(pUpdates) != 3 {
		t.Fatalf("expected 3 patch updates, got %d", len(pUpdates))
	}

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

	handle := func(s state, ev Event) HandleResult[state] {
		if ev.Action == "toggle-help" {
			s.ShowHelp = !s.ShowHelp
		}
		return Result(s)
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

	mUpdates := morphUpdates(mt.updates)
	if len(mUpdates) != 1 {
		t.Fatalf("expected 1 morph update for structural change, got %d", len(mUpdates))
	}
	if len(patchUpdates(mt.updates)) != 0 {
		t.Errorf("expected no patch updates for structural change, got %d", len(patchUpdates(mt.updates)))
	}
}

func TestHandleResultAnnounceMergedIntoUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "increment"},
		},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.handle = func(s counterState, ev Event) HandleResult[counterState] {
		s.Count++
		return Result(s).WithAnnounce("Counter incremented")
	}

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].Announce != "Counter incremented" {
		t.Errorf("Announce = %q, want %q", mt.updates[0].Announce, "Counter incremented")
	}
	if len(mt.updates[0].Patches) != 1 {
		t.Errorf("expected 1 patch alongside announce, got %d", len(mt.updates[0].Patches))
	}
}

func TestHandleResultFlashMergedIntoUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "increment"},
		},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.handle = func(s counterState, ev Event) HandleResult[counterState] {
		s.Count++
		return Result(s).WithFlash("#notice", "Saved!")
	}

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].Flash["#notice"] != "Saved!" {
		t.Errorf("Flash[#notice] = %q, want %q", mt.updates[0].Flash["#notice"], "Saved!")
	}
}

func TestHandleResultNoEffectsNoExtraUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "noop"},
		},
	}

	sess := newTestSession(counterState{Count: 5}, mt)
	sess.equal = func(a, b counterState) bool { return a.Count == b.Count }

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 0 {
		t.Errorf("expected no updates for noop with Equal, got %d", len(mt.updates))
	}
}

func TestHandleResultEffectsSentEvenWhenStateUnchanged(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "noop"},
		},
	}

	sess := newTestSession(counterState{Count: 5}, mt)
	sess.equal = func(a, b counterState) bool { return a.Count == b.Count }
	sess.handle = func(s counterState, ev Event) HandleResult[counterState] {
		return Result(s).WithAnnounce("Still here")
	}

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update for effects despite equal state, got %d", len(mt.updates))
	}
	if mt.updates[0].Announce != "Still here" {
		t.Errorf("Announce = %q, want %q", mt.updates[0].Announce, "Still here")
	}
}

func TestSessionOnDisconnect(t *testing.T) {
	mt := &mockTransport{
		events: []Event{},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	called := false
	sess.onDisconnect = func() { called = true }

	sess.run()

	if !called {
		t.Error("onDisconnect should be called when session ends")
	}
}
