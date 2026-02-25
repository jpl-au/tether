package poly

import "strconv"

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

// FilterKey restricts a KeyDown event to a specific key (e.g. "Enter").
// The event is only sent to the server if the key name matches.
func FilterKey[E settable[E]](el E, key string) E {
	return el.SetData("poly-key", key)
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

// --- Timing ---

// Debounce overrides the default 300ms debounce delay on input events.
// Only meaningful on elements that also have a poly-input binding.
func Debounce[E settable[E]](el E, ms int) E {
	return el.SetData("poly-debounce", strconv.Itoa(ms))
}

// Throttle sets a minimum interval between repeated events. The JS
// runtime drops events that arrive within the throttle window.
func Throttle[E settable[E]](el E, ms int) E {
	return el.SetData("poly-throttle", strconv.Itoa(ms))
}

// --- Loading states ---

// Disable marks an element for automatic disabling while an event is
// in flight. The JS runtime sets the disabled attribute when the event
// is sent and clears it when the next server update arrives. If text
// is non-empty, the element's text content is temporarily replaced
// (e.g. "Saving..." while a form submits).
func Disable[E settable[E]](el E, text string) E {
	return el.SetData("poly-disable", text)
}

// --- Confirmation ---

// Confirm attaches a confirmation prompt to an event-bound element.
// The JS runtime shows window.confirm with the given message before
// sending the event. If the user cancels, the event is dropped.
func Confirm[E settable[E]](el E, message string) E {
	return el.SetData("poly-confirm", message)
}

// --- Form helpers ---

// Preserve prevents the JS runtime from resetting a form's fields after
// submit. Use this when the form is inside a Dynamic key and the server
// controls field values — the morph will clear fields on success and
// preserve them on validation failure.
func Preserve[E settable[E]](el E) E {
	return el.SetData("poly-preserve", "")
}

// --- Focus ---

// AutoFocus marks an element to receive focus after the next server
// update. The JS runtime calls focus() on the first element with
// data-poly-autofocus after applying patches and morphs. Only one
// element should have this attribute at a time. This uses a separate
// attribute from the Focus event binding to avoid collisions.
func AutoFocus[E settable[E]](el E) E {
	return el.SetData("poly-autofocus", "")
}

// FocusTrap constrains Tab key navigation to the element's descendants.
// Useful for modals and dropdown menus.
func FocusTrap[E settable[E]](el E) E {
	return el.SetData("poly-focus-trap", "")
}

// --- JS hooks ---

// Hook annotates an element with a named JS hook. The developer
// registers callbacks on the global Poly.hooks object in JavaScript:
//
//	Poly.hooks.chart = {
//	    mounted: function(el) { /* init */ },
//	    updated: function(el) { /* refresh */ },
//	    destroyed: function(el) { /* teardown */ }
//	};
//
// The JS runtime calls mounted when the element is added to the DOM,
// updated when it is morphed in place, and destroyed when it is about
// to be removed.
func Hook[E settable[E]](el E, name string) E {
	return el.SetData("poly-hook", name)
}

// --- Transitions ---

// Transition annotates an element with a CSS transition name. When the
// element is added to the DOM during a morph, the JS runtime applies
// poly-{name}-enter and removes it next frame. When removed, it applies
// poly-{name}-leave and waits for transitionend before removing the node.
func Transition[E settable[E]](el E, name string) E {
	return el.SetData("poly-transition", name)
}

// --- Signal bindings ---
//
// Signal bindings connect elements to server-pushed reactive values.
// When the server calls [Session.Signal], the client updates all
// bound elements instantly — no render, no diff, no HTML. Bindings
// survive morphs: the client reapplies current signal values after
// idiomorph reconciliation.

// BindText binds an element's text content to a named signal. When
// the server pushes a new value, the client sets textContent directly.
//
//	poly.BindText(span.New(), "count")
func BindText[E settable[E]](el E, signal string) E {
	return el.SetData("poly-bind-text", signal)
}

// BindShow binds an element's visibility to a named signal. The
// element is shown (display restored) when the value is truthy and
// hidden (display:none) when falsy.
//
//	poly.BindShow(div.New(children...), "isOpen")
func BindShow[E settable[E]](el E, signal string) E {
	return el.SetData("poly-bind-show", signal)
}

// BindHide is the inverse of [BindShow]. The element is hidden when
// the value is truthy and shown when falsy.
func BindHide[E settable[E]](el E, signal string) E {
	return el.SetData("poly-bind-hide", signal)
}

// BindClass binds a CSS class to a named signal. The class is added
// when the signal value is truthy and removed when falsy. The data
// attribute stores "className signalName" as a space-separated pair.
//
//	poly.BindClass(span.New(), "active", "isSelected")
func BindClass[E settable[E]](el E, class, signal string) E {
	return el.SetData("poly-bind-class", class+" "+signal)
}

// BindAttr binds an HTML attribute to a named signal. When the signal
// value is falsy (null, false, undefined) the attribute is removed;
// otherwise it is set to the string representation of the value.
//
//	poly.BindAttr(button.New(), "disabled", "isLoading")
func BindAttr[E settable[E]](el E, attr, signal string) E {
	return el.SetData("poly-bind-attr", attr+" "+signal)
}

// Data attaches an arbitrary data value to an element. When an event
// fires from this element, the key and value are included in the
// Event.Data map sent to the server. Use this to avoid encoding IDs
// or other parameters into action strings.
//
// Usage:
//
//	poly.Data(poly.Click(el, "delete"), "id", "123")
//
// On the server:
//
//	if ev.Action == "delete" {
//	    id := ev.Data["id"] // "123"
//	}
func Data[E settable[E]](el E, key, value string) E {
	return el.SetData("poly-data-"+key, value)
}
