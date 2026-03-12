package router

import (
	"sync"
	"testing"

	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	tether "github.com/jpl-au/tether"
)

func TestRouteOverwritesExistingPage(t *testing.T) {
	r := New(selector)
	r.Route("/", Page[state]{Handle: func(_ tether.Session, s state, _ tether.Event) state {
		s.Count = 1
		return s
	}})
	r.Route("/", Page[state]{Handle: func(_ tether.Session, s state, _ tether.Event) state {
		s.Count = 2
		return s
	}})

	got := r.Handle(nil, state{Page: "/"}, tether.Event{})
	if got.Count != 2 {
		t.Fatalf("expected Count=2, got %d", got.Count)
	}
}

func TestOnNavigate(t *testing.T) {
	r := New(selector)
	hp := r.OnNavigate(func(s *state, p tether.Params) { s.Page = p.Path })

	got := hp(nil, state{}, tether.Params{Path: "/settings"})
	if got.Page != "/settings" {
		t.Fatalf("expected Page=/settings, got %q", got.Page)
	}
}

func TestConcurrentRouteAndRender(t *testing.T) {
	r := New(selector)
	r.Route("/", Page[state]{Render: func(s state) node.Node {
		return div.Text("home")
	}})

	var wg sync.WaitGroup
	// Concurrent writers adding routes.
	for i := range 50 {
		wg.Go(func() {
			path := "/" + string(rune('a'+i%26))
			r.Route(path, Page[state]{Render: func(s state) node.Node {
				return span.Text(path)
			}})
		})
	}
	// Concurrent readers dispatching renders.
	for range 100 {
		wg.Go(func() {
			r.Render(state{Page: "/"})
		})
	}
	wg.Wait()
}
