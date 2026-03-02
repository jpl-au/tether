package poly

import (
	"testing"
	"testing/synctest"

	"github.com/jpl-au/fluent-poly/event"
	"github.com/jpl-au/fluent-poly/wire"
)

func TestSignalOutsideHandle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		sess.Signal("count", 42)
		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		if len(mt.sent) == 0 {
			t.Fatal("expected an update with signals")
		}
		msg := decodeMessage(mt.sent[len(mt.sent)-1])
		if msg.Signals == nil {
			t.Fatal("update.Signals is nil")
		}
		if msg.Signals["count"] != float64(42) {
			t.Errorf("Signals[count] = %v, want 42", msg.Signals["count"])
		}
	})
}

func TestSignalInsideHandle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{
			{Type: event.Click, Action: "increment"},
		}}

		handle := func(s PreSession, state counterState, ev Event) counterState {
			s.Signal("status", "active")
			state.Count++
			return state
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = handle

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		// The signal should be merged into the same update as the state diff.
		var found bool
		for _, data := range mt.sent {
			msg := decodeMessage(data)
			if msg.Signals != nil && msg.Signals["status"] == "active" {
				found = true
				break
			}
		}
		if !found {
			t.Error("signal 'status' not found in any update")
		}
	})
}

func TestSignalMultipleKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{
			{Type: event.Click, Action: "increment"},
		}}

		handle := func(s PreSession, state counterState, ev Event) counterState {
			s.Signal("count", 1)
			s.Signal("status", "online")
			s.Signal("count", 2) // overwrite
			return state
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = handle

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		var found bool
		for _, data := range mt.sent {
			msg := decodeMessage(data)
			if msg.Signals == nil {
				continue
			}
			// Last write wins. JSON numbers decode as float64.
			if msg.Signals["count"] == float64(2) && msg.Signals["status"] == "online" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected signals count=2, status=online in update")
		}
	})
}

func TestSignalOnPreSession(t *testing.T) {
	cs := &captureSession{id: "pre", fx: &effects{}}

	cs.Signal("count", 99)
	cs.Signal("status", "ready")

	if cs.fx.signals == nil {
		t.Fatal("captureSession signals is nil")
	}
	if cs.fx.signals["count"] != 99 {
		t.Errorf("signals[count] = %v, want 99", cs.fx.signals["count"])
	}
	if cs.fx.signals["status"] != "ready" {
		t.Errorf("signals[status] = %v, want ready", cs.fx.signals["status"])
	}
}

func TestSignalWithoutStateChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{
			{Type: event.Click, Action: "noop", EventID: "e1"},
		}}

		handle := func(s PreSession, state counterState, ev Event) counterState {
			s.Signal("ping", "pong")
			// State unchanged — signal should still be sent.
			return state
		}

		sess := newTestSession(counterState{Count: 0}, mt)
		sess.handle = handle
		// Enable equal check so the unchanged-state path triggers.
		sess.equal = func(a, b counterState) bool { return a.Count == b.Count }

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		synctest.Wait()

		mt.mu.Lock()
		defer mt.mu.Unlock()

		var found bool
		for _, data := range mt.sent {
			msg := decodeMessage(data)
			if msg.Signals != nil && msg.Signals["ping"] == "pong" {
				found = true
				break
			}
		}
		if !found {
			t.Error("signal should be sent even when state is unchanged")
		}
	})
}

func TestSignalMergedIntoEffects(t *testing.T) {
	fx := &effects{}
	fx.signals = map[string]any{"count": 5}

	if !fx.any() {
		t.Error("effects.any() should be true when signals are set")
	}

	u := &wire.Update{}
	fx.merge(u)

	if u.Signals == nil {
		t.Fatal("wire.Update.Signals is nil after merge")
	}
	if u.Signals["count"] != 5 {
		t.Errorf("Signals[count] = %v, want 5", u.Signals["count"])
	}
}
