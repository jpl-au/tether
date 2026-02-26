package poly

import "testing"

type outerState struct {
	Inner innerState
	Other string
}

type innerState struct {
	Count int
}

var testScope = Scope[outerState, innerState]{
	Get: func(s outerState) innerState { return s.Inner },
	Set: func(s outerState, c innerState) outerState { s.Inner = c; return s },
}

func TestScopeGet(t *testing.T) {
	state := outerState{Inner: innerState{Count: 5}, Other: "hello"}
	sub := testScope.Get(state)
	if sub.Count != 5 {
		t.Errorf("Count = %d, want 5", sub.Count)
	}
}

func TestScopeWith(t *testing.T) {
	state := outerState{Inner: innerState{Count: 3}, Other: "hello"}
	state = testScope.With(state, func(c innerState) innerState {
		c.Count += 10
		return c
	})
	if state.Inner.Count != 13 {
		t.Errorf("Count = %d, want 13", state.Inner.Count)
	}
	if state.Other != "hello" {
		t.Errorf("Other = %q, want hello", state.Other)
	}
}

func TestScopeHandle(t *testing.T) {
	state := outerState{Inner: innerState{Count: 0}, Other: "keep"}
	cs := &captureSession{id: "test", fx: &effects{}}
	ev := Event{Action: "increment"}

	state = testScope.Handle(cs, state, ev, func(sess PreSession, c innerState, ev Event) innerState {
		if ev.Action == "increment" {
			c.Count++
		}
		return c
	})

	if state.Inner.Count != 1 {
		t.Errorf("Count = %d, want 1", state.Inner.Count)
	}
	if state.Other != "keep" {
		t.Errorf("Other = %q, want keep", state.Other)
	}
}

func TestScopeHandleEffects(t *testing.T) {
	state := outerState{Inner: innerState{Count: 0}}
	cs := &captureSession{id: "test", fx: &effects{}}
	ev := Event{Action: "greet"}

	testScope.Handle(cs, state, ev, func(sess PreSession, c innerState, ev Event) innerState {
		sess.Toast("hello")
		sess.Signal("count", 42)
		return c
	})

	if cs.fx.toast != "hello" {
		t.Errorf("toast = %q, want hello", cs.fx.toast)
	}
	if cs.fx.signals["count"] != 42 {
		t.Errorf("signals[count] = %v, want 42", cs.fx.signals["count"])
	}
}
