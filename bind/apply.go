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
//	    bind.WithConfirm("Are you sure?"),
//	    bind.WithDisable("Deleting..."),
//	)
type Option struct {
	key   string
	value string
}

// Apply attaches all options to an element in order. This provides a
// readable top-to-bottom alternative to nesting helpers:
//
//	// Nested (inside-out)
//	bind.Disable(bind.Confirm(bind.Click(btn, "delete"), "Sure?"), "Deleting...")
//
//	// Applied (top-to-bottom)
//	bind.Apply(btn, bind.OnClick("delete"), bind.WithConfirm("Sure?"), bind.WithDisable("Deleting..."))
func Apply[E Settable[E]](el E, opts ...Option) E {
	for _, o := range opts {
		el = el.SetData(o.key, o.value)
	}
	return el
}

// Server event options.

// OnClick binds a poly-click event.
func OnClick(action string) Option { return Option{"poly-click", action} }

// OnSubmit binds a poly-submit event.
func OnSubmit(action string) Option { return Option{"poly-submit", action} }

// OnInput binds a poly-input event (debounced).
func OnInput(action string) Option { return Option{"poly-input", action} }

// OnChange binds a poly-change event.
func OnChange(action string) Option { return Option{"poly-change", action} }

// OnKeyDown binds a poly-keydown event.
func OnKeyDown(action string) Option { return Option{"poly-keydown", action} }

// OnFocus binds a poly-focus event.
func OnFocus(action string) Option { return Option{"poly-focus", action} }

// OnBlur binds a poly-blur event.
func OnBlur(action string) Option { return Option{"poly-blur", action} }

// OnViewport binds a poly-viewport event.
func OnViewport(action string) Option { return Option{"poly-viewport", action} }

// WithEvent binds an arbitrary DOM event. Use this for events not
// covered by the built-in options (OnClick, OnSubmit, etc.).
//
//	bind.Apply(el, bind.WithEvent("dblclick", "open-editor"))
func WithEvent(eventType, action string) Option {
	return Option{"poly-" + eventType, action}
}

// Control options.

// WithDisable disables the element while an event is in flight.
func WithDisable(text string) Option { return Option{"poly-disable", text} }

// WithConfirm shows a confirmation prompt before sending the event.
func WithConfirm(message string) Option { return Option{"poly-confirm", message} }

// WithPreserve prevents form field reset after submit.
func WithPreserve() Option { return Option{"poly-preserve", ""} }

// WithAutoFocus gives the element focus after the next server update.
func WithAutoFocus() Option { return Option{"poly-autofocus", ""} }

// WithIndicator shows a loading indicator at the given selector.
func WithIndicator(selector string) Option { return Option{"poly-indicator", selector} }

// WithFocusTrap traps keyboard focus within the element.
func WithFocusTrap() Option { return Option{"poly-focus-trap", ""} }

// Timing options.

// WithDebounce overrides the default input debounce delay.
func WithDebounce(d time.Duration) Option {
	return Option{"poly-debounce", strconv.Itoa(int(d.Milliseconds()))}
}

// WithThrottle sets a minimum interval between events.
func WithThrottle(d time.Duration) Option {
	return Option{"poly-throttle", strconv.Itoa(int(d.Milliseconds()))}
}

// WithFilterKey restricts a keydown event to a specific key.
func WithFilterKey(key string) Option { return Option{"poly-key", key} }

// WithData sets a custom data-poly-* attribute. Use this for
// attributes that don't have a dedicated With* helper.
func WithData(key, value string) Option { return Option{key, value} }

// WithEventData attaches an extra key-value pair to events.
func WithEventData(key, value string) Option { return Option{"poly-data-" + key, value} }

// Directive options.

// WithLink marks the element for client-side navigation.
func WithLink() Option { return Option{"poly-link", ""} }

// WithToggleClass toggles a CSS class on click.
func WithToggleClass(class string) Option { return Option{"poly-toggle-class", class} }

// WithToggleTarget directs the toggle at a different element.
func WithToggleTarget(selector string) Option { return Option{"poly-toggle-target", selector} }

// WithToggleAttr toggles a boolean attribute on click.
func WithToggleAttr(attr string) Option { return Option{"poly-toggle-attr", attr} }

// WithCloak hides the element until the runtime initialises.
func WithCloak() Option { return Option{"poly-cloak", ""} }

// WithPermanent excludes the element from morphing.
func WithPermanent() Option { return Option{"poly-permanent", ""} }

// Signal binding options.

// WithBindText binds an element's text content to a named signal.
func WithBindText(signal string) Option { return Option{"poly-bind-text", signal} }

// WithBindShow shows an element when the named signal is truthy.
func WithBindShow(signal string) Option { return Option{"poly-bind-show", signal} }

// WithBindHide hides an element when the named signal is truthy.
func WithBindHide(signal string) Option { return Option{"poly-bind-hide", signal} }

// WithBindClass binds a CSS class to a named signal. The class is
// added when truthy and removed when falsy.
func WithBindClass(class, signal string) Option {
	return Option{"poly-bind-class", class + " " + signal}
}

// WithBindAttr binds an HTML attribute to a named signal.
func WithBindAttr(attr, signal string) Option {
	return Option{"poly-bind-attr", attr + " " + signal}
}

// WithBindValue binds a form element's value property to a named signal.
func WithBindValue(signal string) Option { return Option{"poly-bind-value", signal} }

// Signal directive options.

// WithToggleSignal flips a boolean signal on click without a server round-trip.
func WithToggleSignal(signal string) Option { return Option{"poly-toggle-signal", signal} }

// WithSetSignal sets a signal to a specific value on click without a server round-trip.
func WithSetSignal(signal, value string) Option {
	return Option{"poly-set-signal", signal + " " + value}
}

// WithOptimistic sets a signal immediately on click, before the event
// is sent to the server.
func WithOptimistic(signal, value string) Option {
	return Option{"poly-optimistic", signal + " " + value}
}

// WithOptimisticToggle flips a boolean signal immediately on click,
// before the event is sent to the server.
func WithOptimisticToggle(signal string) Option { return Option{"poly-optimistic-toggle", signal} }

// Upload options.

// WithUpload marks the element as an upload trigger.
func WithUpload(action string) Option { return Option{"poly-upload", action} }

// WithUploadInput sets a CSS selector for finding file inputs when the
// upload trigger is not adjacent to them in the DOM.
func WithUploadInput(selector string) Option { return Option{"poly-upload-input", selector} }

// WithUploadProgress binds an element's value attribute to upload progress.
func WithUploadProgress(action string) Option {
	return Option{"poly-bind-attr", "value upload:" + action + ":progress"}
}

// Lifecycle options.

// WithHook attaches a JS lifecycle hook.
func WithHook(name string) Option { return Option{"poly-hook", name} }

// WithTransition enables CSS enter/leave transitions.
func WithTransition(name string) Option { return Option{"poly-transition", name} }
