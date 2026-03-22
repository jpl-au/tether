package tether

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/mode"
)

func TestSessionHandlePanicDestroysSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Single event so readTransport exits cleanly after the
		// panic triggers session destruction.
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "crash"},
			},
		}

		handle := func(_ Session, s counterState, ev Event) counterState {
			if ev.Action == "crash" {
				panic("boom")
			}
			s.Count++
			return s
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = handle

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		if s := sess.State(); s.Count != 0 {
			t.Errorf("expected Count 0 (session destroyed), got %d", s.Count)
		}

		select {
		case <-sess.destroyed:
			// expected
		default:
			t.Error("session should be destroyed after panic")
		}
	})
}

func TestSessionHandlePanicCallsOnPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "crash"},
				{Type: event.Click, Action: "increment"},
			},
		}

		handle := func(_ Session, s counterState, ev Event) counterState {
			if ev.Action == "crash" {
				panic("boom")
			}
			s.Count++
			return s
		}

		var called atomic.Bool
		var gotErr atomic.Value
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = handle
		sess.onPanic = func(_ *StatefulSession[counterState], err error) {
			called.Store(true)
			gotErr.Store(err.Error())
		}

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		if !called.Load() {
			t.Error("OnPanic callback was not called")
		}
		if msg, ok := gotErr.Load().(string); !ok || msg != "boom" {
			t.Errorf("OnPanic error = %q, want %q", msg, "boom")
		}

		// With OnPanic set, the session survives and processes
		// the second event.
		if s := sess.State(); s.Count != 1 {
			t.Errorf("expected Count 1 (session survived), got %d", s.Count)
		}
	})
}

func TestSessionUpdatePanicDestroysSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		sess.Update(func(s counterState) counterState {
			panic("boom in update")
		})
		synctest.Wait()

		select {
		case <-sess.destroyed:
			// expected
		default:
			t.Error("session should be destroyed after panic in Update")
		}
	})
}

func TestSessionUpdatePanicCallsOnPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		var called atomic.Bool
		sess.onPanic = func(_ *StatefulSession[counterState], err error) {
			called.Store(true)
		}

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		sess.Update(func(s counterState) counterState {
			panic("boom in update")
		})
		synctest.Wait()

		if !called.Load() {
			t.Error("OnPanic callback was not called")
		}

		// State should be unchanged - the panicking Update didn't
		// complete, but the session survived.
		if s := sess.State(); s.Count != 0 {
			t.Errorf("expected Count 0 after panicking Update, got %d", s.Count)
		}
	})
}

func TestServeInitialPagePanicDoesNotCrashProcess(t *testing.T) {
	handler := Stateful(App{}, StatefulConfig[counterState]{
		Mode:    mode.WebSocket,
		Upgrade: stubUpgrade,
		InitialState: func(r *http.Request) counterState {
			panic("boom in InitialState")
		},
		Render: renderCounter,
		Handle: handleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()

	// Should not panic - the recovery in serveInitialPage catches it.
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
