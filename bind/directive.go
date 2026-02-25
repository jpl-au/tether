package bind

// Client-side directives run entirely in the browser. The server
// never learns about the state change — it is ephemeral and exists
// only in the client DOM. When a server morph arrives, the JS runtime
// preserves client-managed classes and attributes via an Idiomorph
// beforeNodeMorphed hook.

// Link marks an anchor element for client-side navigation. When
// clicked, the JS runtime intercepts the click, updates the browser
// URL via pushState, and sends a navigate event to the server instead
// of performing a full page load.
func Link[E Settable[E]](el E) E {
	return el.SetData("poly-link", "")
}

// ToggleClass binds a client-side class toggle. On click, the JS
// runtime toggles the named CSS class without a server round-trip.
// Multiple classes can be space-separated.
func ToggleClass[E Settable[E]](el E, class string) E {
	return el.SetData("poly-toggle-class", class)
}

// ToggleTarget directs a toggle at a different element. The value is
// a CSS selector. Without this, toggles apply to the element itself.
func ToggleTarget[E Settable[E]](el E, selector string) E {
	return el.SetData("poly-toggle-target", selector)
}

// ToggleAttr binds a client-side boolean attribute toggle. On click,
// the JS runtime adds or removes the named attribute (e.g. "hidden",
// "aria-expanded") without a server round-trip.
func ToggleAttr[E Settable[E]](el E, attr string) E {
	return el.SetData("poly-toggle-attr", attr)
}
