package tether

import (
	"context"
	"net/url"
	"testing"
	"testing/synctest"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/wire"
)

// composeNav mirrors the composition in handler.go: navigate events
// dispatch to onNavigate, everything else to appHandle.
func composeNav[S any](appHandle HandleFunc[S], onNavigate func(Session, S, Params) S) HandleFunc[S] {
	return func(sess Session, s S, ev Event) S {
		if ev.Type == event.Navigate {
			params := Params{Path: ev.Data["path"]}
			if search := ev.Data["search"]; search != "" {
				v, err := url.ParseQuery(search)
				if err != nil {
					panic("test: malformed query string: " + err.Error())
				}
				params.Query = v
			}
			return onNavigate(sess, s, params)
		}
		return appHandle(sess, s, ev)
	}
}

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

		onNavigate := func(_ Session, s state, params Params) state {
			s.Page = params.Path
			return s
		}

		// Compose OnNavigate into Handle, mirroring handler.go.
		handle := composeNav(
			func(_ Session, s state, _ Event) state { return s },
			onNavigate,
		)

		mt := &mockTransport{
			events: []Event{
				{Type: event.Navigate, Data: map[string]string{"path": "/profile", "search": ""}},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[state]{
			id:        "test",
			state:     state{Page: "/"},
			render:    render,
			handle:    handle,
			differ:    differ,
			encoder:   wire.JSONEncoder{},
			transport: mt,
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*Effects), defaultCmdBufferSize),
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

		if s := sess.State(); s.Page != "/profile" {
			t.Errorf("expected page %q, got %q", "/profile", s.Page)
		}

		mt.mu.Lock()
		defer mt.mu.Unlock()

		if len(mt.sent) == 0 {
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

		onNavigate := func(_ Session, s state, params Params) state {
			s.Page = params.Path
			if params.Query != nil {
				s.Tab = params.Query.Get("tab")
			}
			return s
		}

		handle := composeNav(
			func(_ Session, s state, _ Event) state { return s },
			onNavigate,
		)

		mt := &mockTransport{
			events: []Event{
				{Type: event.Navigate, Data: map[string]string{"path": "/settings", "search": "tab=security"}},
			},
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		sess := &LiveSession[state]{
			id:        "test",
			state:     state{Page: "/"},
			render:    func(s state) node.Node { return div.New(span.Text(s.Page).Dynamic("page")) },
			handle:    handle,
			differ:    differ,
			encoder:   wire.JSONEncoder{},
			transport: mt,
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*Effects), defaultCmdBufferSize),
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

		if s := sess.State(); s.Tab != "security" {
			t.Errorf("expected tab %q, got %q", "security", s.Tab)
		}
	})
}

func TestSessionNavigateEventWithoutOnNavigate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var receivedAction event.Type

		mt := &mockTransport{
			events: []Event{
				{Type: event.Navigate, Data: map[string]string{"path": "/about"}},
			},
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = func(_ Session, s counterState, ev Event) counterState {
			receivedAction = ev.Type
			return s
		}

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if receivedAction != event.Navigate {
			t.Errorf("expected handle to receive navigate event, got %q", receivedAction)
		}
	})
}

func TestSessionNavigateSendsURL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.Navigate("/new-page")
		synctest.Wait()

		ct.mu.Lock()
		defer ct.mu.Unlock()

		if len(ct.sent) != 1 {
			t.Fatalf("expected 1 update, got %d", len(ct.sent))
		}
		msg := decodeMessage(ct.sent[0])
		if msg.URL != "/new-page" {
			t.Errorf("expected URL %q, got %q", "/new-page", msg.URL)
		}
		if msg.Replace {
			t.Error("Navigate should use pushState (Replace=false)")
		}
	})
}

func TestSessionReplaceURLSendsReplace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.ReplaceURL("/current")
		synctest.Wait()

		ct.mu.Lock()
		defer ct.mu.Unlock()

		if len(ct.sent) != 1 {
			t.Fatalf("expected 1 update, got %d", len(ct.sent))
		}
		msg := decodeMessage(ct.sent[0])
		if msg.URL != "/current" {
			t.Errorf("expected URL %q, got %q", "/current", msg.URL)
		}
		if !msg.Replace {
			t.Error("ReplaceURL should use replaceState (Replace=true)")
		}
	})
}

func TestCaptureSessionBuffersEffects(t *testing.T) {
	cs := &CaptureSession{SessionID: "cap-1"}

	cs.SetTitle("Hello")
	cs.Toast("saved")
	cs.Navigate("/next")
	cs.Flash("#msg", "done")
	cs.Announce("submitted")

	if cs.ID() != "cap-1" {
		t.Errorf("ID() = %q, want %q", cs.ID(), "cap-1")
	}
	if cs.Effects.Title != "Hello" {
		t.Errorf("title = %q, want %q", cs.Effects.Title, "Hello")
	}
	if cs.Effects.Toast != "saved" {
		t.Errorf("toast = %q, want %q", cs.Effects.Toast, "saved")
	}
	if cs.Effects.URL != "/next" || cs.Effects.Replace {
		t.Errorf("url = %q replace = %v, want %q false", cs.Effects.URL, cs.Effects.Replace, "/next")
	}
	if cs.Effects.Flash["#msg"] != "done" {
		t.Errorf("flash[#msg] = %q, want %q", cs.Effects.Flash["#msg"], "done")
	}
	if cs.Effects.Announce != "submitted" {
		t.Errorf("announce = %q, want %q", cs.Effects.Announce, "submitted")
	}
}

func TestOnNavigateWithCaptureSession(t *testing.T) {
	type state struct {
		Page  string
		Title string
	}

	onNavigate := func(s Session, st state, params Params) state {
		st.Page = params.Path
		s.SetTitle("My App - " + params.Path)
		return st
	}

	cs := &CaptureSession{SessionID: "pre"}
	result := onNavigate(cs, state{}, Params{Path: "/about"})

	if result.Page != "/about" {
		t.Errorf("Page = %q, want %q", result.Page, "/about")
	}
	if cs.Effects.Title != "My App - /about" {
		t.Errorf("captured title = %q, want %q", cs.Effects.Title, "My App - /about")
	}
}
