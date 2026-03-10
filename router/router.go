// Package router provides multi-page routing for tether. It
// dispatches render and handle calls to the active page based on a
// field in the session state, enabling single-binary applications
// with multiple views.
//
// Create a router with a selector function that returns the current
// page identifier from state, then register pages by path:
//
//	r := router.New[State](func(s State) string { return s.Page })
//	r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
//	r.Route("/settings", router.Page[State]{Render: settingsRender, Handle: settingsHandle})
//	r.NotFound(router.Page[State]{Render: notFoundRender})
//
// Pass r.Render and r.Handle to [tether.Config]:
//
//	tether.New(tether.Config[State]{
//	    Render: r.Render,
//	    Handle: r.Handle,
//	    OnNavigate: r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
//	})
package router

import (
	"maps"
	"sync"
	"sync/atomic"

	tether "github.com/jpl-au/tether"
	"github.com/jpl-au/fluent/node"
)

// Page defines the render and handle logic for a single route.
type Page[S any] struct {
	Render tether.RenderFunc[S]
	Handle tether.HandleFunc[S]
}

// Router manages a set of pages keyed by URL path. It provides
// [tether.HandleFunc] and [tether.RenderFunc] implementations that
// dispatch to the active page based on a field in the session state.
//
// To use Router, your state must have a field that tracks the current
// page (typically a string path). Pass a selector function to [New]
// that returns this field.
//
// The page registry is stored in an [atomic.Value] so dispatch (Render
// and Handle) is completely lock-free. Route and NotFound use a write
// mutex with copy-on-write semantics.
type Router[S any] struct {
	selector func(S) string
	wmu      sync.Mutex
	pages    atomic.Value // holds map[string]Page[S]
	notFound atomic.Value // holds Page[S]
}

// New creates a router that dispatches based on the string returned
// by the selector function.
func New[S any](selector func(S) string) *Router[S] {
	r := &Router[S]{
		selector: selector,
	}
	r.pages.Store(make(map[string]Page[S]))
	r.notFound.Store(Page[S]{})
	return r
}

// Route registers a page for a specific path. Thread-safe via
// copy-on-write.
func (r *Router[S]) Route(path string, page Page[S]) {
	r.wmu.Lock()
	defer r.wmu.Unlock()

	old := r.loadPages()
	newPages := make(map[string]Page[S], len(old)+1)
	maps.Copy(newPages, old)
	newPages[path] = page
	r.pages.Store(newPages)
}

// NotFound sets the page used when the selector returns an unknown
// path. Thread-safe.
func (r *Router[S]) NotFound(page Page[S]) {
	r.notFound.Store(page)
}

// Render implements [tether.RenderFunc]. It dispatches to the active
// page's Render function. Lock-free.
func (r *Router[S]) Render(s S) node.Node {
	path := r.selector(s)
	pages := r.loadPages()
	if p, ok := pages[path]; ok {
		return p.Render(s)
	}
	nf := r.loadNotFound()
	if nf.Render != nil {
		return nf.Render(s)
	}
	return nil
}

// Handle implements [tether.HandleFunc]. It dispatches to the active
// page's Handle function. Lock-free.
func (r *Router[S]) Handle(sess tether.Session, s S, ev tether.Event) S {
	path := r.selector(s)
	pages := r.loadPages()
	if p, ok := pages[path]; ok && p.Handle != nil {
		return p.Handle(sess, s, ev)
	}
	nf := r.loadNotFound()
	if nf.Handle != nil {
		return nf.Handle(sess, s, ev)
	}
	return s
}

func (r *Router[S]) loadPages() map[string]Page[S] {
	return r.pages.Load().(map[string]Page[S])
}

func (r *Router[S]) loadNotFound() Page[S] {
	return r.notFound.Load().(Page[S])
}

// OnNavigate is a convenience helper for [tether.Config].OnNavigate.
// It wraps a simple setter function in the full OnNavigate signature
// that Config expects (func(Session, S, Params) S), handling the
// pointer-to-value dance and return plumbing so the caller only writes
// the state mutation logic. Without this helper, every router user
// would have to write the same boilerplate closure manually.
//
// The setter receives [tether.Params] which carries the URL path and
// query parameters with typed extraction helpers. Params is passed
// through in full — nothing is discarded — so the setter has access
// to both the path and every query parameter.
//
// For simple cases where only the path matters:
//
//	r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path })
//
// For cases that also derive state from query parameters:
//
//	r.OnNavigate(func(s *State, p tether.Params) {
//	    s.Page   = p.Path
//	    s.Filter = p.Get("f")
//	    s.Limit  = p.IntOr("limit", 20)
//	})
func (r *Router[S]) OnNavigate(setter func(*S, tether.Params)) func(tether.Session, S, tether.Params) S {
	return func(_ tether.Session, s S, p tether.Params) S {
		setter(&s, p)
		return s
	}
}
