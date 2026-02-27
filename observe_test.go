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

func TestObserveDeliversCurrentValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := NewValue(42)
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		Observe(v, sess, func(val int, s counterState) counterState {
			s.Count = val
			return s
		})
		synctest.Wait()

		if s := sess.State(); s.Count != 42 {
			t.Errorf("Count = %d, want 42 (current value on subscribe)", s.Count)
		}
	})
}

func TestObserveDeliversFutureChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := NewValue(0)
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		Observe(v, sess, func(val int, s counterState) counterState {
			s.Count = val
			return s
		})
		synctest.Wait()

		v.Store(100)
		synctest.Wait()

		if s := sess.State(); s.Count != 100 {
			t.Errorf("Count = %d, want 100", s.Count)
		}
	})
}

func TestObserveCrossHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type dashState struct {
			Active int
		}

		v := NewValue(5)

		// Session A: counterState
		mtA := &mockTransport{events: []Event{}}
		sessA := newTestSession(counterState{Count: 0}, mtA)
		sessA.id = "A"

		go sessA.readTransport(sessA.events)
		go sessA.run()
		defer func() { sessA.stop(); synctest.Wait() }()

		Observe(v, sessA, func(val int, s counterState) counterState {
			s.Count = val
			return s
		})
		synctest.Wait()

		// Session B: dashState (different state type)
		differB := jit.NewDiffer()
		renderDash := func(s dashState) node.Node {
			return div.New(span.Text("dash").Dynamic("d"))
		}
		ctxB, cancelB := newTestContext()
		sessB := &Session[dashState]{
			id:        "B",
			state:     dashState{},
			render:    renderDash,
			handle:    func(_ *Session[dashState], s dashState, _ Event) dashState { return s },
			differ:    differB,
			transport: &mockTransport{events: []Event{}},
			logger:    slog.Default(),
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*effects), defaultCmdBufferSize),
			loopDone:  make(chan struct{}),
			ctx:       ctxB,
			stop:      cancelB,
		}
		differB.Render(renderDash(dashState{}))

		go sessB.readTransport(sessB.events)
		go sessB.run()
		defer func() { sessB.stop(); synctest.Wait() }()

		Observe(v, sessB, func(val int, s dashState) dashState {
			s.Active = val
			return s
		})
		synctest.Wait()

		// Both should have the initial value.
		if s := sessA.State(); s.Count != 5 {
			t.Errorf("A.Count = %d, want 5", s.Count)
		}
		if s := sessB.State(); s.Active != 5 {
			t.Errorf("B.Active = %d, want 5", s.Active)
		}

		// Store a new value — both should update.
		v.Store(20)
		synctest.Wait()

		if s := sessA.State(); s.Count != 20 {
			t.Errorf("A.Count after Store = %d, want 20", s.Count)
		}
		if s := sessB.State(); s.Active != 20 {
			t.Errorf("B.Active after Store = %d, want 20", s.Active)
		}
	})
}

func TestObserveAutoCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := NewValue(0)
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()

		Observe(v, sess, func(val int, s counterState) counterState {
			s.Count = val
			return s
		})
		synctest.Wait()

		if v.Len() != 1 {
			t.Fatalf("Len() = %d, want 1", v.Len())
		}

		sess.stop()
		synctest.Wait()

		if v.Len() != 0 {
			t.Fatalf("Len() after stop = %d, want 0", v.Len())
		}
	})
}

func TestObserveMultipleValues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type state struct {
			A int
			B string
		}

		vA := NewValue(10)
		vB := NewValue("hello")

		render := func(s state) node.Node {
			return div.New(span.Text("x").Dynamic("x"))
		}
		differ := jit.NewDiffer()
		ctx, cancel := newTestContext()
		mt := &mockTransport{events: []Event{}}
		sess := &Session[state]{
			id:        "multi",
			state:     state{},
			render:    render,
			handle:    func(_ *Session[state], s state, _ Event) state { return s },
			differ:    differ,
			transport: mt,
			logger:    slog.Default(),
			events:    make(chan Event),
			cmds:      make(chan func(), defaultCmdBufferSize),
			fxCh:      make(chan func(*effects), defaultCmdBufferSize),
			loopDone:  make(chan struct{}),
			ctx:       ctx,
			stop:      cancel,
		}
		differ.Render(render(state{}))

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		Observe(vA, sess, func(val int, s state) state {
			s.A = val
			return s
		})
		Observe(vB, sess, func(val string, s state) state {
			s.B = val
			return s
		})
		synctest.Wait()

		s := sess.State()
		if s.A != 10 {
			t.Errorf("A = %d, want 10", s.A)
		}
		if s.B != "hello" {
			t.Errorf("B = %q, want %q", s.B, "hello")
		}

		vA.Store(99)
		vB.Store("world")
		synctest.Wait()

		s = sess.State()
		if s.A != 99 {
			t.Errorf("A after Store = %d, want 99", s.A)
		}
		if s.B != "world" {
			t.Errorf("B after Store = %q, want %q", s.B, "world")
		}
	})
}

// newTestContext creates a cancellable context for test sessions.
func newTestContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
