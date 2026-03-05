// Package bind provides element annotation helpers for fluent-tether.
// Each helper attaches a data-tether-* attribute to a Fluent element,
// telling the client JS runtime how to handle that element — which
// events to forward, what client-side behaviour to apply, or which
// reactive signals to bind.
//
// All bindings are applied via [Apply] with composable [Option] values:
//
//	bind.Apply(button.Text("Delete"),
//	    bind.OnClick("delete"),
//	    bind.Confirm("Are you sure?"),
//	    bind.Disable("Deleting..."),
//	)
//
// This top-to-bottom style scales cleanly as behaviours are stacked
// and provides a single, consistent way to annotate elements.
package bind

// Settable is the structural type constraint for element annotation
// helpers. Any Fluent element with a chainable SetData method satisfies
// it.
type Settable[E any] interface {
	SetData(string, string) E
}
