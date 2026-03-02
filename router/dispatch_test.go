package router

import (
	"testing"

	tether "github.com/jpl-au/fluent-tether"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func TestRenderDispatchesToMatchingPage(t *testing.T) {
	r := New(selector)
	r.Route("/", Page[state]{Render: func(s state) node.Node {
		return div.Text("home")
	}})
	r.Route("/about", Page[state]{Render: func(s state) node.Node {
		return span.Text("about")
	}})

	got := r.Render(state{Page: "/about"})
	if got == nil {
		t.Fatal("expected non-nil node")
	}
}

func TestRenderFallsBackToNotFound(t *testing.T) {
	r := New(selector)
	r.Route("/", Page[state]{Render: func(s state) node.Node {
		return div.Text("home")
	}})
	r.NotFound(Page[state]{Render: func(s state) node.Node {
		return span.Text("404")
	}})

	got := r.Render(state{Page: "/missing"})
	if got == nil {
		t.Fatal("expected NotFound node, got nil")
	}
}

func TestRenderReturnsNilWithoutNotFound(t *testing.T) {
	r := New(selector)

	got := r.Render(state{Page: "/missing"})
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestHandleDispatchesToMatchingPage(t *testing.T) {
	r := New(selector)
	r.Route("/", Page[state]{Handle: func(_ tether.PreSession, s state, _ tether.Event) state {
		s.Count = 42
		return s
	}})

	got := r.Handle(nil, state{Page: "/"}, tether.Event{})
	if got.Count != 42 {
		t.Fatalf("expected Count=42, got %d", got.Count)
	}
}

func TestHandleFallsBackToNotFound(t *testing.T) {
	r := New(selector)
	r.NotFound(Page[state]{Handle: func(_ tether.PreSession, s state, _ tether.Event) state {
		s.Count = -1
		return s
	}})

	got := r.Handle(nil, state{Page: "/missing"}, tether.Event{})
	if got.Count != -1 {
		t.Fatalf("expected Count=-1, got %d", got.Count)
	}
}

func TestHandleReturnsStateWhenNoMatch(t *testing.T) {
	r := New(selector)

	s := state{Page: "/missing", Count: 7}
	got := r.Handle(nil, s, tether.Event{})
	if got.Count != 7 {
		t.Fatalf("expected Count=7, got %d", got.Count)
	}
}

func TestHandleReturnsStateWhenHandlerNil(t *testing.T) {
	r := New(selector)
	r.Route("/", Page[state]{Render: func(s state) node.Node {
		return div.Text("home")
	}})

	s := state{Page: "/", Count: 5}
	got := r.Handle(nil, s, tether.Event{})
	if got.Count != 5 {
		t.Fatalf("expected Count=5, got %d", got.Count)
	}
}
