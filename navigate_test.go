package poly

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func TestSessionNavigateEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type state struct {
			Page string
		}

		render := func(s state) node.Node {
			return div.New(
				span.Text(s.Page).Dynamic("page"),
			)
		}

		handleParams := func(_ *Session[state], s state, params Params) state {
			s.Page = params.Path
			return s
		}

		mt := &mockTransport{
			events: []Event{
				{Type: "navigate", Data: map[string]string{"path": "/profile", "search": ""}},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &Session[state]{
			id:           "test",
			state:        state{Page: "/"},
			render:       render,
			handle:       func(_ *Session[state], s state, ev Event) state { return s },
			handleParams: handleParams,
			differ:       differ,
			transport:    mt,
			logger:       slog.Default(),
			events:       make(chan Event),
			cmds:         make(chan func(), cmdBufferSize),
			ctx:          ctx,
			stop:         cancel,
		}

		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if s := sess.State(); s.Page != "/profile" {
			t.Errorf("expected page %q, got %q", "/profile", s.Page)
		}

		mt.mu.Lock()
		defer mt.mu.Unlock()

		if len(mt.updates) == 0 {
			t.Fatal("expected at least one update after navigation")
		}
	})
}

func TestSessionNavigateEventWithQuery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type state struct {
			Page string
			Tab  string
		}

		handleParams := func(_ *Session[state], s state, params Params) state {
			s.Page = params.Path
			if params.Query != nil {
				s.Tab = params.Query.Get("tab")
			}
			return s
		}

		mt := &mockTransport{
			events: []Event{
				{Type: "navigate", Data: map[string]string{"path": "/settings", "search": "tab=security"}},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &Session[state]{
			id:           "test",
			state:        state{Page: "/"},
			render:       func(s state) node.Node { return div.New(span.Text(s.Page).Dynamic("page")) },
			handle:       func(_ *Session[state], s state, ev Event) state { return s },
			handleParams: handleParams,
			differ:       differ,
			transport:    mt,
			logger:       slog.Default(),
			events:       make(chan Event),
			cmds:         make(chan func(), cmdBufferSize),
			ctx:          ctx,
			stop:         cancel,
		}

		tree := sess.render(sess.state)
		differ.Render(tree)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if s := sess.State(); s.Tab != "security" {
			t.Errorf("expected tab %q, got %q", "security", s.Tab)
		}
	})
}

func TestSessionNavigateEventWithoutHandleParams(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var receivedAction string

		mt := &mockTransport{
			events: []Event{
				{Type: "navigate", Data: map[string]string{"path": "/about"}},
			},
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = func(_ *Session[counterState], s counterState, ev Event) counterState {
			receivedAction = ev.Type
			return s
		}

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if receivedAction != "navigate" {
			t.Errorf("expected handle to receive navigate event, got %q", receivedAction)
		}
	})
}

func TestSessionNavigateSendsURL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.Navigate("/new-page")
		synctest.Wait()

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
	})
}

func TestSessionReplaceURLSendsReplace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.ReplaceURL("/current")
		synctest.Wait()

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
	})
}
