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
