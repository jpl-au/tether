// Package router provides multi-page routing for fluent-poly. It
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
// Pass r.Render and r.Handle to [poly.Config]:
//
//	poly.New(poly.Config[State]{
//	    Render: r.Render,
//	    Handle: r.Handle,
//	    HandleParams: r.HandleParams(func(s *State, path string) { s.Page = path }),
//	})
package router

import (
	"sync"
	"sync/atomic"

	poly "github.com/jpl-au/fluent-poly"
	"github.com/jpl-au/fluent/node"
)

// Page defines the render and handle logic for a single route.
type Page[S any] struct {
	Render poly.RenderFunc[S]
	Handle poly.HandleFunc[S]
}

// Router manages a set of pages keyed by URL path. It provides
// [poly.HandleFunc] and [poly.RenderFunc] implementations that
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
	for k, v := range old {
		newPages[k] = v
	}
	newPages[path] = page
	r.pages.Store(newPages)
}

// NotFound sets the page used when the selector returns an unknown
// path. Thread-safe.
func (r *Router[S]) NotFound(page Page[S]) {
	r.notFound.Store(page)
}

// Render implements [poly.RenderFunc]. It dispatches to the active
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

// Handle implements [poly.HandleFunc]. It dispatches to the active
// page's Handle function. Lock-free.
func (r *Router[S]) Handle(sess *poly.Session[S], s S, ev poly.Event) S {
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

// HandleParams is a helper for [poly.Config].HandleParams that simply
// updates the page field in the state.
func (r *Router[S]) HandleParams(setter func(*S, string)) func(poly.PreSession, S, poly.Params) S {
	return func(_ poly.PreSession, s S, p poly.Params) S {
		setter(&s, p.Path)
		return s
	}
}
