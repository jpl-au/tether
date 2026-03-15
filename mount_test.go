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

type mountState struct {
	Widget testWidget
	Other  string
}

func renderMountState(s mountState) node.Node {
	return div.New(
		span.Textf("count:%d", s.Widget.Count).Dynamic("count"),
		span.Textf("other:%s", s.Other).Dynamic("other"),
	)
}

func handleMountState(_ Session, s mountState, ev Event) mountState {
	if ev.Action == "set-other" {
		s.Other = ev.Value()
	}
	return s
}

func newMountSession(state mountState, mt Transport, mounts []ComponentMount[mountState]) *LiveSession[mountState] {
	differ := jit.NewDiffer()
	ctx, cancel := context.WithCancel(context.Background())
	sess := &LiveSession[mountState]{
		id:        "test",
		state:     state,
		render:    renderMountState,
		handle:    handleMountState,
		differ:    differ,
		encoder:   wire.JSONEncoder{},
		transport: mt,
		events:    make(chan Event),
		cmds:      make(chan func(), defaultCmdBufferSize),
		fxCh:      make(chan func(*Effects), defaultCmdBufferSize),
		loopDone:  make(chan struct{}),
		ctx:       ctx,
		stop:      cancel,
		mounts:    mounts,
	}
	tree := sess.render(sess.state)
	differ.Render(tree)
	return sess
}

func TestMountRouteDispatchesToComponent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "widget.inc"},
			},
		}

		mounts := []ComponentMount[mountState]{
			Mount("widget",
				func(s mountState) testWidget { return s.Widget },
				func(s mountState, w testWidget) mountState { s.Widget = w; return s },
			),
		}

		sess := newMountSession(mountState{}, mt, mounts)
		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		got := sess.State()
		if got.Widget.Count != 1 {
			t.Errorf("Widget.Count = %d, want 1", got.Widget.Count)
		}
	})
}

func TestMountRouteSkipsNonMatchingEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "set-other", Data: map[string]string{"value": "hello"}},
			},
		}

		mounts := []ComponentMount[mountState]{
			Mount("widget",
				func(s mountState) testWidget { return s.Widget },
				func(s mountState, w testWidget) mountState { s.Widget = w; return s },
			),
		}

		sess := newMountSession(mountState{}, mt, mounts)
		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		got := sess.State()
		if got.Other != "hello" {
			t.Errorf("Other = %q, want hello", got.Other)
		}
		if got.Widget.Count != 0 {
			t.Errorf("Widget.Count = %d, want 0 (should not be touched)", got.Widget.Count)
		}
	})
}

func TestMountRouteSetsEventTarget(t *testing.T) {
	// Verify that the component mount sets Event.Target so
	// middleware/logging can identify which component handled it.
	var captured string

	type targetState struct {
		Widget targetWidget
	}

	mounts := []ComponentMount[targetState]{
		Mount("chat",
			func(s targetState) targetWidget { return s.Widget },
			func(s targetState, w targetWidget) targetState { s.Widget = w; return s },
		),
	}

	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "chat.send"},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[targetState]{
			id:     "test",
			state:  targetState{Widget: targetWidget{onHandle: func(ev Event) { captured = ev.Target }}},
			render: func(s targetState) node.Node { return div.New(span.Text("x").Dynamic("x")) },
			handle: func(_ Session, s targetState, ev Event) targetState { return s },
			differ: differ, encoder: wire.JSONEncoder{},
			transport: mt, events: make(chan Event),
			cmds:     make(chan func(), defaultCmdBufferSize),
			fxCh:     make(chan func(*Effects), defaultCmdBufferSize),
			loopDone: make(chan struct{}),
			ctx:      ctx, stop: cancel,
			mounts: mounts,
		}
		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if captured != "chat" {
			t.Errorf("Event.Target = %q, want chat", captured)
		}
	})
}

// targetWidget captures the event for inspection.
type targetWidget struct {
	onHandle func(Event)
}

func (w targetWidget) Render() node.Node { return span.Text("target") }
func (w targetWidget) Handle(sess Session, ev Event) Component {
	if w.onHandle != nil {
		w.onHandle(ev)
	}
	return w
}

// mountableWidget implements Mounter to verify the framework calls
// Mount once during session startup.
type mountableWidget struct {
	Count   int
	Mounted bool
}

func (w mountableWidget) Render() node.Node {
	return span.Textf("count:%d mounted:%v", w.Count, w.Mounted).Dynamic("mw")
}

func (w mountableWidget) Handle(_ Session, ev Event) Component {
	if ev.Action == "inc" {
		w.Count++
	}
	return w
}

func (w mountableWidget) Mount(sess Session) Component {
	sess.Toast("mounted")
	w.Mounted = true
	return w
}

func TestMounterCalledOnInit(t *testing.T) {
	state := mountState{Widget: testWidget{}}
	mw := mountableWidget{}

	type mState struct {
		MW mountableWidget
	}

	mounts := []ComponentMount[mState]{
		Mount("mw",
			func(s mState) mountableWidget { return s.MW },
			func(s mState, w mountableWidget) mState { s.MW = w; return s },
		),
	}

	s := mState{MW: mw}
	sess := &CaptureSession{SessionID: "test"}
	result := InitMounts(mounts, sess, s)

	if !result.MW.Mounted {
		t.Error("MW.Mounted = false, want true after InitMounts")
	}
	if sess.Effects.Toast != "mounted" {
		t.Errorf("toast = %q, want %q", sess.Effects.Toast, "mounted")
	}

	// Verify non-Mounter components are left unchanged.
	_ = state // suppress unused
}

func TestInitMountsSkipsNonMounter(t *testing.T) {
	// testWidget does not implement Mounter — InitMounts should not panic.
	mounts := []ComponentMount[mountState]{
		Mount("widget",
			func(s mountState) testWidget { return s.Widget },
			func(s mountState, w testWidget) mountState { s.Widget = w; return s },
		),
	}
	s := mountState{Widget: testWidget{Count: 5}}
	result := InitMounts(mounts, &CaptureSession{SessionID: "test"}, s)

	if result.Widget.Count != 5 {
		t.Errorf("Widget.Count = %d, want 5 (should be unchanged)", result.Widget.Count)
	}
}

func TestMountNavigateBypassesMounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				// Navigate events should go to Handle, not mounts.
				{Type: event.Navigate, Action: "widget.inc"},
			},
		}

		mounts := []ComponentMount[mountState]{
			Mount("widget",
				func(s mountState) testWidget { return s.Widget },
				func(s mountState, w testWidget) mountState { s.Widget = w; return s },
			),
		}

		sess := newMountSession(mountState{}, mt, mounts)
		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		got := sess.State()
		// The mount should NOT have handled this — Count stays 0.
		if got.Widget.Count != 0 {
			t.Errorf("Widget.Count = %d, want 0 (navigate should bypass mounts)", got.Widget.Count)
		}
	})
}
