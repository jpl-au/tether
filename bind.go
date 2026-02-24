package poly

// Event binding helpers. Each function wraps SetData with the correct
// data-poly-* attribute so callers don't need to remember the convention
// strings. The underlying SetData call is unchanged — these are purely
// convenience.
//
// Usage:
//
//	poly.Click(button.Text("+"), "increment").Style("cursor: pointer")
//	poly.Submit(form.New(children...), "save")
//
// The generic constraint ensures these only compile on types that have
// a chainable SetData method (i.e. any Fluent element).

// settable is the structural type constraint for event binding helpers.
// Any Fluent element with a chainable SetData method satisfies it.
type settable[E any] interface {
	SetData(string, string) E
}

// Click binds a poly-click event. When the element is clicked, the
// server receives an Event with the given action.
func Click[E settable[E]](el E, action string) E {
	return el.SetData("poly-click", action)
}

// Submit binds a poly-submit event. When the form is submitted, the
// server receives an Event with form field values in Data.
func Submit[E settable[E]](el E, action string) E {
	return el.SetData("poly-submit", action)
}

// Input binds a poly-input event. Fires as the user types, debounced
// at 300ms by default (configurable via data-poly-debounce on the element).
func Input[E settable[E]](el E, action string) E {
	return el.SetData("poly-input", action)
}

// Change binds a poly-change event. Fires when a form control's value
// is committed (e.g. select change, checkbox toggle).
func Change[E settable[E]](el E, action string) E {
	return el.SetData("poly-change", action)
}

// KeyDown binds a poly-keydown event. The key name is sent in
// Event.Data["key"].
func KeyDown[E settable[E]](el E, action string) E {
	return el.SetData("poly-keydown", action)
}

// Focus binds a poly-focus event.
func Focus[E settable[E]](el E, action string) E {
	return el.SetData("poly-focus", action)
}

// Blur binds a poly-blur event.
func Blur[E settable[E]](el E, action string) E {
	return el.SetData("poly-blur", action)
}

// --- Navigation ---

// Link marks an anchor element for client-side navigation. When clicked,
// the JS runtime intercepts the click, updates the browser URL via
// pushState, and sends a navigate event to the server instead of
// performing a full page load.
func Link[E settable[E]](el E) E {
	return el.SetData("poly-link", "")
}

// --- Client-side directives ---
//
// These run entirely in the browser. The server never learns about
// the toggle state — it is ephemeral and exists only in the client DOM.
// When a server morph arrives, the JS runtime preserves client-managed
// classes and attributes via an Idiomorph beforeNodeMorphed hook.

// ToggleClass binds a client-side class toggle. On click, the JS runtime
// toggles the named CSS class without a server round-trip. Multiple
// classes can be space-separated.
func ToggleClass[E settable[E]](el E, class string) E {
	return el.SetData("poly-toggle-class", class)
}

// ToggleTarget directs a toggle at a different element. The value is a
// CSS selector. Without this, toggles apply to the element itself.
func ToggleTarget[E settable[E]](el E, selector string) E {
	return el.SetData("poly-toggle-target", selector)
}

// ToggleAttr binds a client-side boolean attribute toggle. On click, the
// JS runtime adds or removes the named attribute (e.g. "hidden",
// "aria-expanded") without a server round-trip.
func ToggleAttr[E settable[E]](el E, attr string) E {
	return el.SetData("poly-toggle-attr", attr)
}

// --- Form helpers ---

// Preserve prevents the JS runtime from resetting a form's fields after
// submit. Use this when the form is inside a Dynamic key and the server
// controls field values — the morph will clear fields on success and
// preserve them on validation failure.
func Preserve[E settable[E]](el E) E {
	return el.SetData("poly-preserve", "")
}

// --- Transitions ---

// Transition annotates an element with a CSS transition name. When the
// element is added to the DOM during a morph, the JS runtime applies
// poly-{name}-enter and removes it next frame. When removed, it applies
// poly-{name}-leave and waits for transitionend before removing the node.
func Transition[E settable[E]](el E, name string) E {
	return el.SetData("poly-transition", name)
}
