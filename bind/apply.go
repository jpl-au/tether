package bind

import (
	"strconv"
	"time"
)

// Option describes a single data attribute to apply to an element.
// Use with [Apply] for a top-to-bottom composition style when
// stacking multiple behaviours on one element:
//
//	bind.Apply(button.Text("Delete"),
//	    bind.OnClick("delete"),
//	    bind.Confirm("Are you sure?"),
//	    bind.Disable("Deleting..."),
//	)
type Option struct {
	key   string
	value string
}

// Apply attaches all options to an element in order. Stack multiple
// behaviours top-to-bottom for readable composition:
//
//	bind.Apply(btn, bind.OnClick("delete"), bind.Confirm("Sure?"), bind.Disable("Deleting..."))
func Apply[E Settable[E]](el E, opts ...Option) E {
	for _, o := range opts {
		el = el.SetData(o.key, o.value)
	}
	return el
}

// Server event options.

// OnClick binds a tether-click event.
func OnClick(action string) Option { return Option{"tether-click", action} }

// OnSubmit binds a tether-submit event.
func OnSubmit(action string) Option { return Option{"tether-submit", action} }

// OnInput binds a tether-input event (debounced).
func OnInput(action string) Option { return Option{"tether-input", action} }

// OnChange binds a tether-change event.
func OnChange(action string) Option { return Option{"tether-change", action} }

// OnKeyDown binds a tether-keydown event.
func OnKeyDown(action string) Option { return Option{"tether-keydown", action} }

// OnFocus binds a tether-focus event.
func OnFocus(action string) Option { return Option{"tether-focus", action} }

// OnBlur binds a tether-blur event.
func OnBlur(action string) Option { return Option{"tether-blur", action} }

// OnViewport binds a tether-viewport event.
func OnViewport(action string) Option { return Option{"tether-viewport", action} }

// Event binds an arbitrary DOM event. Use this for events not
// covered by the built-in options (OnClick, OnSubmit, etc.).
//
//	bind.Apply(el, bind.Event("dblclick", "open-editor"))
func Event(eventType, action string) Option {
	return Option{"tether-" + eventType, action}
}

// Control options.

// Disable disables the element while an event is in flight.
func Disable(text string) Option { return Option{"tether-disable", text} }

// Confirm shows a confirmation prompt before sending the event.
func Confirm(message string) Option { return Option{"tether-confirm", message} }

// Reset resets form fields after submit.
func Reset() Option { return Option{"tether-reset", ""} }

// AutoFocus gives the element focus after the next server update.
func AutoFocus() Option { return Option{"tether-autofocus", ""} }

// Indicator shows a loading indicator at the given selector.
func Indicator(selector string) Option { return Option{"tether-indicator", selector} }

// FocusTrap traps keyboard focus within the element.
func FocusTrap() Option { return Option{"tether-focus-trap", ""} }

// Timing options.

// Debounce overrides the default input debounce delay.
func Debounce(d time.Duration) Option {
	return Option{"tether-debounce", strconv.Itoa(int(d.Milliseconds()))}
}

// Throttle sets a minimum interval between events.
func Throttle(d time.Duration) Option {
	return Option{"tether-throttle", strconv.Itoa(int(d.Milliseconds()))}
}

// FilterKey restricts a keydown event to a specific key.
func FilterKey(key string) Option { return Option{"tether-key", key} }

// Data sets a custom data-tether-* attribute.
func Data(key, value string) Option { return Option{key, value} }

// EventData attaches an extra key-value pair to events.
func EventData(key, value string) Option { return Option{"tether-data-" + key, value} }

// Directive options.

// Link marks the element for client-side navigation.
func Link() Option { return Option{"tether-link", ""} }

// ToggleClass toggles a CSS class on click.
func ToggleClass(class string) Option { return Option{"tether-toggle-class", class} }

// ToggleTarget directs the toggle at a different element.
func ToggleTarget(selector string) Option { return Option{"tether-toggle-target", selector} }

// ToggleAttr toggles a boolean attribute on click.
func ToggleAttr(attr string) Option { return Option{"tether-toggle-attr", attr} }

// Cloak hides the element until the runtime initialises.
func Cloak() Option { return Option{"tether-cloak", ""} }

// Permanent excludes the element from morphing.
func Permanent() Option { return Option{"tether-permanent", ""} }

// Signal binding options.

// BindText binds an element's text content to a named signal.
func BindText(signal string) Option { return Option{"tether-bind-text", signal} }

// BindShow shows an element when the named signal is truthy.
func BindShow(signal string) Option { return Option{"tether-bind-show", signal} }

// BindHide hides an element when the named signal is truthy.
func BindHide(signal string) Option { return Option{"tether-bind-hide", signal} }

// BindClass binds a CSS class to a named signal. The class is
// added when truthy and removed when falsy.
func BindClass(class, signal string) Option {
	return Option{"tether-bind-class", class + " " + signal}
}

// BindAttr binds an HTML attribute to a named signal.
func BindAttr(attr, signal string) Option {
	return Option{"tether-bind-attr", attr + " " + signal}
}

// BindValue binds a form element's value property to a named signal.
func BindValue(signal string) Option { return Option{"tether-bind-value", signal} }

// Signal directive options.

// ToggleSignal flips a boolean signal on click without a server round-trip.
func ToggleSignal(signal string) Option { return Option{"tether-toggle-signal", signal} }

// SetSignal sets a signal to a specific value on click without a server round-trip.
func SetSignal(signal, value string) Option {
	return Option{"tether-set-signal", signal + " " + value}
}

// Optimistic sets a signal immediately on click, before the event
// is sent to the server.
func Optimistic(signal, value string) Option {
	return Option{"tether-optimistic", signal + " " + value}
}

// OptimisticToggle flips a boolean signal immediately on click,
// before the event is sent to the server.
func OptimisticToggle(signal string) Option { return Option{"tether-optimistic-toggle", signal} }

// Collect adds a CSS selector that the client resolves at event
// time. Matched elements contribute their current value to Event.Data,
// keyed by the element's name or id attribute. Use this to send input
// values with a click or keydown event without wrapping in a form:
//
//	bind.Apply(button.New().Text("Send"),
//	    bind.OnClick("chat.send"),
//	    bind.Collect("#message-input"),
//	)
func Collect(selector string) Option { return Option{"tether-collect", selector} }

// Upload options.

// Upload marks the element as an upload trigger.
func Upload(action string) Option { return Option{"tether-upload", action} }

// UploadInput sets a CSS selector for finding file inputs when the
// upload trigger is not adjacent to them in the DOM.
func UploadInput(selector string) Option { return Option{"tether-upload-input", selector} }

// UploadProgress binds an element's value attribute to upload progress.
func UploadProgress(action string) Option {
	return Option{"tether-bind-attr", "value upload:" + action + ":progress"}
}

// Push options.

// PushSubscribe marks a button for Web Push subscription. The browser
// requests notification permission on click and subscribes via the
// service worker's PushManager.
func PushSubscribe() Option { return Option{"tether-push-subscribe", ""} }

// Lifecycle options.

// Hook attaches a JS lifecycle hook.
func Hook(name string) Option { return Option{"tether-hook", name} }

// Transition enables CSS enter/leave transitions.
func Transition(name string) Option { return Option{"tether-transition", name} }
