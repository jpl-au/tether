package poly

import (
	"context"
	"testing"
	"testing/synctest"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent-poly/event"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
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

		handle := func(_ PreSession, s state, ev Event) state {
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
		sess := &Session[state]{
			id:        "test",
			state:     state{Count: 0, ShowHelp: false},
			render:    render,
			handle:    handle,
			differ:    differ,
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

func TestSessionOnDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}

		sess := newTestSession(counterState{Count: 0}, mt)
		called := false
		sess.onDisconnect = func() { called = true }

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if !called {
			t.Error("onDisconnect should be called when transport closes")
		}
	})
}
