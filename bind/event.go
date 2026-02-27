package bind

import (
	"strconv"
	"time"
)

// Click binds a poly-click event. When the element is clicked, the
// server receives an Event with the given action.
func Click[E Settable[E]](el E, action string) E {
	return el.SetData("poly-click", action)
}

// Submit binds a poly-submit event. When the form is submitted, the
// server receives an Event with form field values in Data.
func Submit[E Settable[E]](el E, action string) E {
	return el.SetData("poly-submit", action)
}

// Input binds a poly-input event. Fires as the user types, debounced
// at 300ms by default (configurable via [Debounce] on the element).
func Input[E Settable[E]](el E, action string) E {
	return el.SetData("poly-input", action)
}

// Change binds a poly-change event. Fires when a form control's value
// is committed (e.g. select change, checkbox toggle).
func Change[E Settable[E]](el E, action string) E {
	return el.SetData("poly-change", action)
}

// KeyDown binds a poly-keydown event. The key name is sent in
// Event.Data["key"].
func KeyDown[E Settable[E]](el E, action string) E {
	return el.SetData("poly-keydown", action)
}

// FilterKey restricts a KeyDown event to a specific key (e.g. "Enter").
// The event is only sent to the server if the key name matches.
func FilterKey[E Settable[E]](el E, key string) E {
	return el.SetData("poly-key", key)
}

// Focus binds a poly-focus event.
func Focus[E Settable[E]](el E, action string) E {
	return el.SetData("poly-focus", action)
}

// Blur binds a poly-blur event.
func Blur[E Settable[E]](el E, action string) E {
	return el.SetData("poly-blur", action)
}

// EventData attaches an arbitrary data value to an element. When an
// event fires from this element, the key and value are included in the
// Event.Data map sent to the server. Use this to avoid encoding IDs
// or other parameters into action strings.
//
//	bind.EventData(bind.Click(el, "delete"), "id", "123")
func EventData[E Settable[E]](el E, key, value string) E {
	return el.SetData("poly-data-"+key, value)
}

// Debounce overrides the default 300ms debounce delay on input events.
// Only meaningful on elements that also have an [Input] binding.
func Debounce[E Settable[E]](el E, d time.Duration) E {
	return el.SetData("poly-debounce", strconv.Itoa(int(d.Milliseconds())))
}

// Throttle sets a minimum interval between repeated events. The JS
// runtime drops events that arrive within the throttle window.
func Throttle[E Settable[E]](el E, d time.Duration) E {
	return el.SetData("poly-throttle", strconv.Itoa(int(d.Milliseconds())))
}

// On binds an arbitrary DOM event. Use this for events not covered by
// the built-in helpers (Click, Submit, Input, etc.). The eventType is
// appended to the "poly-" prefix, so On(el, "mouseover", "hover")
// sets data-poly-mouseover="hover".
//
//	bind.On(div.New(), "dblclick", "open-editor")
func On[E Settable[E]](el E, eventType, action string) E {
	return el.SetData("poly-"+eventType, action)
}

// Viewport fires a server event when the element enters the viewport.
// Uses IntersectionObserver internally. The event fires once per
// element appearance; after a server morph replaces the element, the
// new element is observed again automatically. This is the building
// block for infinite scroll and lazy-loaded sections.
//
//	bind.Viewport(div.New(), "load-more")
func Viewport[E Settable[E]](el E, action string) E {
	return el.SetData("poly-viewport", action)
}
