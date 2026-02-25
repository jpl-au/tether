// Package router provides multi-page routing for fluent-poly. It
// dispatches render and handle calls to the active page based on a
// field in the session state, enabling single-binary applications
// with multiple views.
//
// Create a router with a selector function that returns the current
// page identifier from state, then register pages by path:
//
//	r := router.New[State](func(s State) string { return s.Page })
//	r.Route("/", homeRender, homeHandle)
//	r.Route("/settings", settingsRender, settingsHandle)
//	r.NotFound(notFoundRender, nil)
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
type Router[S any] struct {
	selector func(S) string
	pages    map[string]Page[S]
	notFound Page[S]
}

// New creates a router that dispatches based on the string returned
// by the selector function.
func New[S any](selector func(S) string) *Router[S] {
	return &Router[S]{
		selector: selector,
		pages:    make(map[string]Page[S]),
	}
}

// Route registers a page for a specific path.
func (r *Router[S]) Route(path string, render poly.RenderFunc[S], handle poly.HandleFunc[S]) {
	r.pages[path] = Page[S]{Render: render, Handle: handle}
}

// NotFound sets the page used when the selector returns an unknown
// path.
func (r *Router[S]) NotFound(render poly.RenderFunc[S], handle poly.HandleFunc[S]) {
	r.notFound = Page[S]{Render: render, Handle: handle}
}

// Render implements [poly.RenderFunc]. It dispatches to the active
// page's Render function.
func (r *Router[S]) Render(s S) node.Node {
	path := r.selector(s)
	if p, ok := r.pages[path]; ok {
		return p.Render(s)
	}
	if r.notFound.Render != nil {
		return r.notFound.Render(s)
	}
	return nil
}

// Handle implements [poly.HandleFunc]. It dispatches to the active
// page's Handle function.
func (r *Router[S]) Handle(sess *poly.Session[S], s S, ev poly.Event) S {
	path := r.selector(s)
	if p, ok := r.pages[path]; ok && p.Handle != nil {
		return p.Handle(sess, s, ev)
	}
	if r.notFound.Handle != nil {
		return r.notFound.Handle(sess, s, ev)
	}
	return s
}

// HandleParams is a helper for [poly.Config].HandleParams that simply
// updates the page field in the state.
func (r *Router[S]) HandleParams(setter func(*S, string)) func(poly.PreSession, S, poly.Params) S {
	return func(_ poly.PreSession, s S, p poly.Params) S {
		setter(&s, p.Path)
		return s
	}
}
