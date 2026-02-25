package poly

import "github.com/jpl-au/fluent/node"

// Page defines the render and handle logic for a single route.
type Page[S any] struct {
	Render RenderFunc[S]
	Handle HandleFunc[S]
}

// Router manages a set of pages keyed by URL path. It provides
// [HandleFunc] and [RenderFunc] implementations that dispatch to the
// active page based on a field in the session state.
//
// To use Router, your state must have a field that tracks the current
// page (typically a string path). Pass a selector function to
// [NewRouter] that returns this field.
type Router[S any] struct {
	selector func(S) string
	pages    map[string]Page[S]
	notFound Page[S]
}

// NewRouter creates a router that dispatches based on the string
// returned by the selector function.
func NewRouter[S any](selector func(S) string) *Router[S] {
	return &Router[S]{
		selector: selector,
		pages:    make(map[string]Page[S]),
	}
}

// Route registers a page for a specific path.
func (r *Router[S]) Route(path string, render RenderFunc[S], handle HandleFunc[S]) {
	r.pages[path] = Page[S]{Render: render, Handle: handle}
}

// NotFound sets the page used when the selector returns an unknown path.
func (r *Router[S]) NotFound(render RenderFunc[S], handle HandleFunc[S]) {
	r.notFound = Page[S]{Render: render, Handle: handle}
}

// Render implements [RenderFunc]. It dispatches to the active page's
// Render function.
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

// Handle implements [HandleFunc]. It dispatches to the active page's
// Handle function.
func (r *Router[S]) Handle(sess *Session[S], s S, ev Event) HandleResult[S] {
	path := r.selector(s)
	if p, ok := r.pages[path]; ok && p.Handle != nil {
		return p.Handle(sess, s, ev)
	}
	if r.notFound.Handle != nil {
		return r.notFound.Handle(sess, s, ev)
	}
	return Result(s)
}

// HandleParams is a helper for [Config.HandleParams] that simply
// updates the page field in the state.
func (r *Router[S]) HandleParams(setter func(*S, string)) func(S, Params) HandleResult[S] {
	return func(s S, p Params) HandleResult[S] {
		setter(&s, p.Path)
		return Result(s)
	}
}
