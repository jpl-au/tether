package tether

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/wire"
)

func TestSessionSendsPatchOnChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "increment"},
			},
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		pMsgs := patchMessages(mt.sent)
		if len(pMsgs) != 1 {
			t.Fatalf("expected 1 patch update, got %d", len(pMsgs))
		}
		if len(pMsgs[0].Patches) != 1 {
			t.Fatalf("expected 1 patch in first update, got %d", len(pMsgs[0].Patches))
		}
		if pMsgs[0].Patches[0].Key != "count" {
			t.Errorf("patch key should be %q, got %q", "count", pMsgs[0].Patches[0].Key)
		}
		if !mt.closed {
			t.Error("transport should be closed after event loop exits")
		}
	})
}

func TestSessionNoPatchWhenUnchanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "noop"},
			},
		}

		sess := newTestSession(counterState{Count: 5}, mt)
		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		if len(patchMessages(mt.sent)) != 0 {
			t.Errorf("expected no patch updates when state unchanged, got %d", len(patchMessages(mt.sent)))
		}
	})
}

func TestSessionEqualSkipsDiff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "noop"},
			},
		}

		sess := newTestSession(counterState{Count: 5}, mt)
		sess.equal = func(a, b counterState) bool {
			return a.Count == b.Count
		}

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		if len(mt.sent) != 0 {
			t.Errorf("expected no updates when Equal returns true, got %d", len(mt.sent))
		}
	})
}

func TestSessionMultipleEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "increment"},
				{Type: event.Click, Action: "increment"},
				{Type: event.Click, Action: "increment"},
			},
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		pMsgs := patchMessages(mt.sent)
		if len(pMsgs) != 3 {
			t.Fatalf("expected 3 patch updates, got %d", len(pMsgs))
		}

		// Read state through the command channel.
		if s := sess.State(); s.Count != 3 {
			t.Errorf("final state should have Count 3, got %d", s.Count)
		}
	})
}

func TestSessionStructuralChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		handle := func(_ Session, s state, ev Event) state {
			if ev.Action == "toggle-help" {
				s.ShowHelp = !s.ShowHelp
			}
			return s
		}

		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "toggle-help"},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &StatefulSession[state]{
			id:        "test",
			state:     state{Count: 0, ShowHelp: false},
			render:    render,
			handle:    handle,
			engine:    differ,
			encoder:   wire.JSONEncoder{},
			transport: mt,
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*Effects), defaultCmdBufferSize),
			loopDone:  make(chan struct{}),
			destroyed: make(chan struct{}),
			ctx:       ctx,
			stop:      cancel,
		}
		sess.status.Store(int32(Pending))

		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		mMsgs := morphMessages(mt.sent)
		if len(mMsgs) != 1 {
			t.Fatalf("expected 1 morph update for structural change, got %d", len(mMsgs))
		}
		if len(patchMessages(mt.sent)) != 0 {
			t.Errorf("expected no patch updates for structural change, got %d", len(patchMessages(mt.sent)))
		}
	})
}

func TestSessionDisconnectCallsHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		var called bool
		h := &Handler[counterState]{
			app: App{},
			cfg: StatefulConfig[counterState]{
				OnDisconnect: func(_ *StatefulSession[counterState]) {
					called = true
				},
			},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       map[string]*StatefulSession[counterState]{sess.id: sess},
			disconnected: make(map[string]*StatefulSession[counterState]),
			done:         make(chan struct{}),
		}
		h.Diagnostics = NewBus[Diagnostic]()
		sess.handler = h

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if !called {
			t.Error("OnDisconnect should be called when transport closes")
		}
	})
}

func TestDisconnectTimerCallsHandlerSessionTimedOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// mockTransport with no events disconnects immediately,
		// triggering onTransportClose which starts the timer.
		mt := &mockTransport{}
		sess := newTestSession(counterState{}, mt)
		sess.reconnectTimeout = 100 * time.Millisecond

		h := &Handler[counterState]{
			app:          App{},
			cfg:          StatefulConfig[counterState]{},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       map[string]*StatefulSession[counterState]{sess.id: sess},
			disconnected: make(map[string]*StatefulSession[counterState]),
			done:         make(chan struct{}),
		}
		h.Diagnostics = NewBus[Diagnostic]()
		sess.handler = h

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Transport has disconnected. The handler moved the
		// session to the disconnected pool and started the timer.
		h.mu.Lock()
		_, inDisc := h.disconnected[sess.id]
		h.mu.Unlock()
		if !inDisc {
			t.Fatal("session should be in disconnected pool after transport close")
		}

		// Advance past the reconnect timeout.
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()

		// The handler's sessionTimedOut should have removed the
		// session from the disconnected pool and destroyed it.
		h.mu.Lock()
		_, stillDisc := h.disconnected[sess.id]
		h.mu.Unlock()

		if stillDisc {
			t.Error("session should be removed from disconnected pool after timeout")
		}

		select {
		case <-sess.ctx.Done():
			// expected - session destroyed
		default:
			t.Error("session context should be cancelled after timeout")
		}
	})
}

func TestStateCalledDuringHandleWarns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var stateCalledDuringHandle bool
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "read-state"},
			},
		}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = func(s Session, state counterState, ev Event) counterState {
			if ev.Action == "read-state" {
				// This should trigger a dev-mode warning.
				live := s.(*StatefulSession[counterState])
				_ = live.State()
				stateCalledDuringHandle = true
			}
			return state
		}

		// Enable dev mode so the warning fires.
		dev.Enable()
		defer dev.Reset()

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if !stateCalledDuringHandle {
			t.Error("handler should have called State()")
		}
		// The warning is emitted via dev.Warn - we verify it
		// doesn't panic and the handling flag is correctly
		// managed (cleared after Handle returns).
		if sess.handling {
			t.Error("handling flag should be false after Handle returns")
		}
	})
}

func TestCommandDiscardedOnDestroyedSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)

		diag := NewBus[Diagnostic]()
		sess.diagnostics = diag

		// Use a separate context so the subscriber outlives
		// the session destruction.
		diagCtx, diagCancel := context.WithCancel(context.Background())
		defer diagCancel()

		var discards int
		diag.Subscribe(diagCtx, func(d Diagnostic) {
			if d.Kind == CommandDiscarded {
				discards++
			}
		})

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Destroy the session.
		sess.stop()
		synctest.Wait()

		// Commands to a destroyed session should emit
		// CommandDiscarded, not silently disappear.
		sess.enqueue(func() {})
		sess.enqueueFx(func(*Effects) {})

		if discards != 2 {
			t.Errorf("expected 2 CommandDiscarded diagnostics on destroyed session, got %d", discards)
		}
	})
}
