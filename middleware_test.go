package tether

import "testing"

func TestChainAppliesOutermostFirst(t *testing.T) {
	var order []string

	a := func(next HandleFunc[counterState]) HandleFunc[counterState] {
		return func(sess Session, s counterState, ev Event) counterState {
			order = append(order, "A")
			return next(sess, s, ev)
		}
	}
	b := func(next HandleFunc[counterState]) HandleFunc[counterState] {
		return func(sess Session, s counterState, ev Event) counterState {
			order = append(order, "B")
			return next(sess, s, ev)
		}
	}
	inner := func(_ Session, s counterState, _ Event) counterState {
		order = append(order, "H")
		return s
	}

	h := Chain(inner, []Middleware[counterState]{a, b})
	h(nil, counterState{}, Event{})

	if len(order) != 3 || order[0] != "A" || order[1] != "B" || order[2] != "H" {
		t.Errorf("expected [A B H], got %v", order)
	}
}

func TestChainEmptyMiddleware(t *testing.T) {
	called := false
	inner := func(_ Session, s counterState, _ Event) counterState {
		called = true
		return s
	}

	h := Chain(inner, nil)
	h(nil, counterState{}, Event{})

	if !called {
		t.Error("inner handler was not called")
	}
}
