// Package bind provides element annotation helpers for fluent-tether.
// Each function attaches a data-tether-* attribute to a Fluent element,
// telling the client JS runtime how to handle that element — which
// events to forward, what client-side behaviour to apply, or which
// reactive signals to bind.
//
// All helpers are generic over any type with a chainable SetData
// method, so they work with every Fluent element type.
//
// Server event bindings (event.go):
//
//	bind.Click(button.Text("+"), "increment")
//	bind.Submit(form.New(children...), "save")
//
// Client-side directives (directive.go):
//
//	bind.Link(a.Link("/profile", "Profile"))
//	bind.ToggleClass(button.Text("Menu"), "is-open")
//
// Reactive signal bindings (signal.go):
//
//	bind.BindText(span.New(), "count")
//	bind.BindShow(div.New(children...), "isOpen")
package bind

// Settable is the structural type constraint for element annotation
// helpers. Any Fluent element with a chainable SetData method satisfies
// it.
type Settable[E any] interface {
	SetData(string, string) E
}
