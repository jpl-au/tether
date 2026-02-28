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

// Cloak hides an element until the poly runtime initialises. The JS
// removes the attribute on DOMContentLoaded, revealing the element.
// A built-in style rule ensures zero flash without any extra CSS.
//
//	bind.Cloak(div.New(children...))
func Cloak[E Settable[E]](el E) E {
	return el.SetData("poly-cloak", "")
}

// Permanent prevents the morph engine from updating this element.
// The element and its entire subtree are left untouched across server
// updates. Use this for video players, iframes, canvas elements, and
// third-party widget containers that manage their own DOM.
//
//	bind.Permanent(div.New(bind.Hook(canvas.New(), "chart")))
func Permanent[E Settable[E]](el E) E {
	return el.SetData("poly-permanent", "")
}

// ToggleSignal flips a boolean signal on click without a server
// round-trip. All signal bindings ([BindShow], [BindClass], etc.)
// react instantly. The server can override the value at any time
// via [Session.Signal].
//
//	bind.ToggleSignal(button.Text("Menu"), "menuOpen")
func ToggleSignal[E Settable[E]](el E, signal string) E {
	return el.SetData("poly-toggle-signal", signal)
}

// SetSignal sets a signal to a specific value on click without a
// server round-trip. Use this for tab bars, radio-style selection,
// and any UI where clicking picks one value from a set.
//
//	bind.SetSignal(button.Text("Settings"), "tab", "settings")
func SetSignal[E Settable[E]](el E, signal, value string) E {
	return el.SetData("poly-set-signal", signal+" "+value)
}

// Optimistic sets a signal to a value immediately on click, before
// the event is sent to the server. When the server responds, its
// signals overwrite the optimistic value — if the prediction was
// wrong, the DOM corrects itself. Use this for predictable mutations
// where the round-trip delay would feel sluggish.
//
//	bind.Click(bind.Optimistic(button.Text("Like"), "liked", "true"), "like")
func Optimistic[E Settable[E]](el E, signal, value string) E {
	return el.SetData("poly-optimistic", signal+" "+value)
}

// PushSubscribe marks an element as the trigger for push notification
// subscription. When clicked, the JS runtime prompts the user for
// notification permission and subscribes via the service worker's
// PushManager. This ensures the browser permission dialog appears in
// response to a genuine user gesture, as required by browser policy.
//
//	bind.PushSubscribe(button.Text("Enable notifications"))
func PushSubscribe[E Settable[E]](el E) E {
	return el.SetData("poly-push-subscribe", "")
}

// OptimisticToggle flips a boolean signal immediately on click,
// before the event is sent to the server. More natural than
// [Optimistic] when you don't know the current value — the JS
// reads the signal and inverts it.
//
//	bind.Click(bind.OptimisticToggle(button.Text("Like"), "liked"), "like")
func OptimisticToggle[E Settable[E]](el E, signal string) E {
	return el.SetData("poly-optimistic-toggle", signal)
}
