package router

import (
	"sync"
	"testing"

	poly "github.com/jpl-au/fluent-poly"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func TestRouteOverwritesExistingPage(t *testing.T) {
	r := New(selector)
	r.Route("/", Page[state]{Handle: func(_ *poly.Session[state], s state, _ poly.Event) state {
		s.Count = 1
		return s
	}})
	r.Route("/", Page[state]{Handle: func(_ *poly.Session[state], s state, _ poly.Event) state {
		s.Count = 2
		return s
	}})

	got := r.Handle(nil, state{Page: "/"}, poly.Event{})
	if got.Count != 2 {
		t.Fatalf("expected Count=2, got %d", got.Count)
	}
}

func TestOnNavigate(t *testing.T) {
	r := New(selector)
	hp := r.OnNavigate(func(s *state, path string) { s.Page = path })

	got := hp(nil, state{}, poly.Params{Path: "/settings"})
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := "/" + string(rune('a'+i%26))
			r.Route(path, Page[state]{Render: func(s state) node.Node {
				return span.Text(path)
			}})
		}()
	}
	// Concurrent readers dispatching renders.
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Render(state{Page: "/"})
		}()
	}
	wg.Wait()
}
