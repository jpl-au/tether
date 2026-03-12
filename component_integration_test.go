package tether

import (
	"context"
	"testing"
	"testing/synctest"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/wire"
)

// todoItem is a single todo entry.
type todoItem struct {
	Text string
	Done bool
}

// todoList is a realistic component exercising the full Component
// surface: multi-action Handle, Render with Dynamic keys, and
// session side-effects.
type todoList struct {
	prefix string
	Items  []todoItem
	Input  string
}

func (t todoList) Render() node.Node {
	var items []node.Node
	for i, item := range t.Items {
		text := item.Text
		if item.Done {
			text = "[x] " + text
		}
		items = append(items, span.Textf("%d: %s", i, text))
	}
	items = append(items,
		span.Text(t.Input).Dynamic(t.prefix+"-input"),
		span.Textf("%d items", len(t.Items)).Dynamic(t.prefix+"-count"),
	)
	return div.New(items...)
}

func (t todoList) Handle(sess Session, ev Event) Component {
	switch ev.Action {
	case "type":
		t.Input = ev.Value()
	case "add":
		if t.Input != "" {
			t.Items = append(t.Items, todoItem{Text: t.Input})
			t.Input = ""
			sess.Toast("Added!")
			sess.Signal(t.prefix+"-count", len(t.Items))
		}
	case "toggle":
		idx, err := ev.Int("index")
		if err == nil && idx >= 0 && idx < len(t.Items) {
			// Copy slice to preserve value semantics.
			t.Items = append([]todoItem{}, t.Items...)
			t.Items[idx].Done = !t.Items[idx].Done
		}
	case "clear":
		var kept []todoItem
		for _, item := range t.Items {
			if !item.Done {
				kept = append(kept, item)
			}
		}
		t.Items = kept
		sess.Signal(t.prefix+"-count", len(t.Items))
	}
	return t
}

// EqualComponent provides a fast equality check — only compare
// item count and input, not the full slice contents.
func (t todoList) EqualComponent(other Component) bool {
	o, ok := other.(todoList)
	if !ok {
		return false
	}
	if len(t.Items) != len(o.Items) || t.Input != o.Input {
		return false
	}
	for i := range t.Items {
		if t.Items[i] != o.Items[i] {
			return false
		}
	}
	return true
}

// --- Multi-instance state with two todo lists ---

type dualTodoState struct {
	Work     todoList
	Personal todoList
}

func renderDualTodo(s dualTodoState) node.Node {
	return div.New(
		div.New(s.Work.Render()).Dynamic("work-section"),
		div.New(s.Personal.Render()).Dynamic("personal-section"),
	)
}

func handleDualTodo(_ Session, s dualTodoState, ev Event) dualTodoState {
	return s
}

// TestComponentIntegrationMultiInstance validates multi-instance
// component dispatch through Config.Components. Two todo lists
// mounted with different prefixes receive events independently.
func TestComponentIntegrationMultiInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				// Add to work list.
				{Type: event.Input, Action: "work.type", Data: map[string]string{"value": "write tests"}},
				{Type: event.Click, Action: "work.add"},
				// Add to personal list.
				{Type: event.Input, Action: "personal.type", Data: map[string]string{"value": "buy milk"}},
				{Type: event.Click, Action: "personal.add"},
				// Add another to work list.
				{Type: event.Input, Action: "work.type", Data: map[string]string{"value": "review PR"}},
				{Type: event.Click, Action: "work.add"},
			},
		}

		mounts := []ComponentMount[dualTodoState]{
			Mount("work",
				func(s dualTodoState) todoList { return s.Work },
				func(s dualTodoState, w todoList) dualTodoState { s.Work = w; return s },
			),
			Mount("personal",
				func(s dualTodoState) todoList { return s.Personal },
				func(s dualTodoState, p todoList) dualTodoState { s.Personal = p; return s },
			),
		}

		initial := dualTodoState{
			Work:     todoList{prefix: "work"},
			Personal: todoList{prefix: "personal"},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[dualTodoState]{
			id:        "test",
			state:     initial,
			render:    renderDualTodo,
			handle:    handleDualTodo,
			differ:    differ,
			encoder:   wire.JSONEncoder{},
			transport: mt,
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*effects), defaultCmdBufferSize),
			loopDone:  make(chan struct{}),
			ctx:       ctx,
			stop:      cancel,
			mounts:    mounts,
		}
		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		got := sess.State()

		if len(got.Work.Items) != 2 {
			t.Errorf("Work.Items = %d, want 2", len(got.Work.Items))
		}
		if len(got.Personal.Items) != 1 {
			t.Errorf("Personal.Items = %d, want 1", len(got.Personal.Items))
		}

		if got.Work.Items[0].Text != "write tests" {
			t.Errorf("Work.Items[0] = %q, want write tests", got.Work.Items[0].Text)
		}
		if got.Work.Items[1].Text != "review PR" {
			t.Errorf("Work.Items[1] = %q, want review PR", got.Work.Items[1].Text)
		}
		if got.Personal.Items[0].Text != "buy milk" {
			t.Errorf("Personal.Items[0] = %q, want buy milk", got.Personal.Items[0].Text)
		}

		// Input should be cleared after add.
		if got.Work.Input != "" {
			t.Errorf("Work.Input = %q, want empty after add", got.Work.Input)
		}
		if got.Personal.Input != "" {
			t.Errorf("Personal.Input = %q, want empty after add", got.Personal.Input)
		}
	})
}

// TestComponentIntegrationSideEffects verifies that session side-effects
// (Toast, Signal) from within a component's Handle are sent to the
// transport.
func TestComponentIntegrationSideEffects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Input, Action: "work.type", Data: map[string]string{"value": "deploy"}},
				{Type: event.Click, Action: "work.add"},
			},
		}

		mounts := []ComponentMount[dualTodoState]{
			Mount("work",
				func(s dualTodoState) todoList { return s.Work },
				func(s dualTodoState, w todoList) dualTodoState { s.Work = w; return s },
			),
		}

		initial := dualTodoState{Work: todoList{prefix: "work"}}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[dualTodoState]{
			id:        "test",
			state:     initial,
			render:    renderDualTodo,
			handle:    handleDualTodo,
			differ:    differ,
			encoder:   wire.JSONEncoder{},
			transport: mt,
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*effects), defaultCmdBufferSize),
			loopDone:  make(chan struct{}),
			ctx:       ctx,
			stop:      cancel,
			mounts:    mounts,
		}
		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		// The "add" event should produce a toast and signal.
		var foundToast, foundSignal bool
		for _, data := range mt.sent {
			msg := decodeMessage(data)
			if msg.Toast == "Added!" {
				foundToast = true
			}
			if v, ok := msg.Signals["work-count"]; ok && v.(float64) == 1 {
				foundSignal = true
			}
		}

		if !foundToast {
			t.Error("expected Toast(\"Added!\") in transport messages")
		}
		if !foundSignal {
			t.Error("expected Signal(\"work-count\", 1) in transport messages")
		}
	})
}

// TestComponentIntegrationToggleAndClear exercises the toggle and clear
// actions to verify slice mutation with value semantics.
func TestComponentIntegrationToggleAndClear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				// Toggle first item done.
				{Type: event.Click, Action: "work.toggle", Data: map[string]string{"index": "0"}},
				// Clear completed items.
				{Type: event.Click, Action: "work.clear"},
			},
		}

		mounts := []ComponentMount[dualTodoState]{
			Mount("work",
				func(s dualTodoState) todoList { return s.Work },
				func(s dualTodoState, w todoList) dualTodoState { s.Work = w; return s },
			),
		}

		initial := dualTodoState{
			Work: todoList{
				prefix: "work",
				Items: []todoItem{
					{Text: "done task"},
					{Text: "keep task"},
				},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[dualTodoState]{
			id:        "test",
			state:     initial,
			render:    renderDualTodo,
			handle:    handleDualTodo,
			differ:    differ,
			encoder:   wire.JSONEncoder{},
			transport: mt,
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*effects), defaultCmdBufferSize),
			loopDone:  make(chan struct{}),
			ctx:       ctx,
			stop:      cancel,
			mounts:    mounts,
		}
		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		got := sess.State()
		if len(got.Work.Items) != 1 {
			t.Fatalf("Work.Items = %d, want 1 after clear", len(got.Work.Items))
		}
		if got.Work.Items[0].Text != "keep task" {
			t.Errorf("remaining item = %q, want keep task", got.Work.Items[0].Text)
		}
	})
}

// TestComponentManualRouteTyped validates that RouteTyped works correctly
// for manual routing in a Handle function (the non-Config.Components path).
func TestComponentManualRouteTyped(t *testing.T) {
	type appState struct {
		Todo todoList
	}

	render := func(s appState) node.Node {
		return div.New(s.Todo.Render())
	}

	handle := func(sess Session, s appState, ev Event) appState {
		s.Todo = RouteTyped(s.Todo, "todo", sess, ev)
		return s
	}

	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Input, Action: "todo.type", Data: map[string]string{"value": "test item"}},
				{Type: event.Click, Action: "todo.add"},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[appState]{
			id:        "test",
			state:     appState{Todo: todoList{prefix: "todo"}},
			render:    render,
			handle:    handle,
			differ:    differ,
			encoder:   wire.JSONEncoder{},
			transport: mt,
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*effects), defaultCmdBufferSize),
			loopDone:  make(chan struct{}),
			ctx:       ctx,
			stop:      cancel,
		}
		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		got := sess.State()
		if len(got.Todo.Items) != 1 {
			t.Fatalf("Todo.Items = %d, want 1", len(got.Todo.Items))
		}
		if got.Todo.Items[0].Text != "test item" {
			t.Errorf("item text = %q, want test item", got.Todo.Items[0].Text)
		}
	})
}

// TestComponentEqualComponentSkipsRender verifies that when the parent's
// Equal function delegates to EqualComponent, unchanged components skip
// the render pipeline.
func TestComponentEqualComponentSkipsRender(t *testing.T) {
	type appState struct {
		Todo todoList
	}

	render := func(s appState) node.Node {
		return div.New(s.Todo.Render())
	}

	handle := func(sess Session, s appState, ev Event) appState {
		s.Todo = RouteTyped(s.Todo, "todo", sess, ev)
		return s
	}

	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				// "noop" action — component returns itself unchanged.
				{Type: event.Click, Action: "todo.noop"},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[appState]{
			id:     "test",
			state:  appState{Todo: todoList{prefix: "todo"}},
			render: render,
			handle: handle,
			differ: differ, encoder: wire.JSONEncoder{},
			transport: mt, events: make(chan Event),
			cmds:     make(chan func(), defaultCmdBufferSize),
			fxCh:     make(chan func(*effects), defaultCmdBufferSize),
			loopDone: make(chan struct{}),
			ctx:      ctx, stop: cancel,
			equal: func(a, b appState) bool {
				return a.Todo.EqualComponent(b.Todo)
			},
		}
		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		// No patches or morphs should be sent — Equal returned true.
		if len(mt.sent) != 0 {
			t.Errorf("expected no updates when EqualComponent returns true, got %d", len(mt.sent))
		}
	})
}
