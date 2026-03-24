package bind

import (
	"strconv"
	"strings"
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

// Server event options - each sends a [tether.Event] to the server's
// Handle function when the corresponding DOM event fires on the element.
// The action string becomes [tether.Event].Action, which Handle switches
// on to determine what happened.

// Prefix sets the event namespace for a component container. When the
// client JS sends an event from inside a prefixed container, it
// automatically prepends the prefix to the action. This allows
// components to use bare action names (e.g. "send") while the
// framework routes them via the full prefixed name (e.g.
// "shoutbox.send").
//
// Apply Prefix to the element that wraps a mounted component's
// Render output:
//
//	bind.Apply(div.New(s.Chat.Render()), bind.Prefix("chat")).Dynamic("chat-section")
func Prefix(name string) Option { return Option{"tether-prefix", name} }

// OnClick forwards click events to the server. The action identifies
// which click this is (e.g. "delete", "save") so Handle can switch on it.
func OnClick(action string) Option { return Option{"tether-click", action} }

// OnSubmit forwards form submissions to the server. All named fields
// inside the form are automatically collected into [tether.Event].Data
// so the handler can read them by name (ev.Data["email"], ev.Int("age")).
func OnSubmit(action string) Option { return Option{"tether-submit", action} }

// OnInput forwards input events to the server with debouncing. The
// element's current value is included automatically in
// [tether.Event].Data["value"]. Debounce delay defaults to 300ms
// (configurable via [tether.App].Client.DefaultDebounce or per-element
// via [Debounce]).
func OnInput(action string) Option { return Option{"tether-input", action} }

// OnChange forwards change events to the server. Unlike [OnInput], this
// fires once when the user commits a value (leaving a text field,
// selecting a dropdown option, toggling a checkbox). The element's value
// is included in [tether.Event].Data["value"].
func OnChange(action string) Option { return Option{"tether-change", action} }

// OnKeyDown forwards keydown events to the server. The pressed key name
// (e.g. "Enter", "Escape", "ArrowUp") is available via
// [tether.Event].Key(). Combine with [FilterKey] to restrict which
// keys trigger the server event.
func OnKeyDown(action string) Option { return Option{"tether-keydown", action} }

// OnFocus forwards focus events to the server. Fires when the element
// receives keyboard focus (click, tab, or programmatic focus).
func OnFocus(action string) Option { return Option{"tether-focus", action} }

// OnBlur forwards blur events to the server. Fires when the element
// loses keyboard focus. Useful for validating input on exit.
func OnBlur(action string) Option { return Option{"tether-blur", action} }

// OnPaste forwards paste events to the server. The pasted text is
// included in [tether.Event].Data["value"]. Use this for
// paste-to-search, paste-to-import, or paste-from-clipboard features.
func OnPaste(action string) Option { return Option{"tether-paste", action} }

// OnViewport fires when the element enters the visible viewport, using
// an IntersectionObserver internally. Place this on a sentinel element
// at the bottom of a list to implement infinite scroll - when the
// sentinel scrolls into view, the server loads the next page of data.
func OnViewport(action string) Option { return Option{"tether-viewport", action} }

// Event forwards an arbitrary DOM event to the server. Use this for
// event types not covered by the built-in options (OnClick, OnSubmit,
// etc.). The eventType is the standard DOM event name.
//
//	bind.Apply(el, bind.Event("dblclick", "open-editor"))
func Event(eventType, action string) Option {
	return Option{"tether-" + eventType, action}
}

// Control options - modify how events behave without changing which
// events are sent. Stack these with event options via [Apply].

// Disable replaces the element's text and disables it while the server
// processes the event. The original text and state are restored when
// the server responds. Prevents double-clicks and gives users visual
// feedback that something is happening.
func Disable(text string) Option { return Option{"tether-disable", text} }

// Confirm shows a browser confirmation dialog before the event is sent.
// If the user cancels, the event is silently dropped.
func Confirm(message string) Option { return Option{"tether-confirm", message} }

// PreventDefault suppresses the browser's default behaviour for the
// event. Use this with [Event] to handle events like contextmenu
// without the browser's native menu appearing:
//
//	bind.Apply(el, bind.Event("contextmenu", "menu.open"), bind.PreventDefault())
func PreventDefault() Option { return Option{"tether-prevent-default", ""} }

// Reset clears all form fields after a successful submit. Useful for
// chat-style inputs where the form should be ready for the next message
// immediately after sending.
func Reset() Option { return Option{"tether-reset", ""} }

// AutoFocus moves keyboard focus to this element after each server
// update morphs the DOM. Use this on the primary input in a form so
// the cursor returns there after each interaction.
func AutoFocus() Option { return Option{"tether-autofocus", ""} }

// Indicator shows a loading spinner or skeleton at the given CSS
// selector while the server processes the event. The indicator is
// removed when the server responds. Use this for actions where the
// user needs visual feedback in a different part of the page from the
// triggering element.
func Indicator(selector string) Option { return Option{"tether-indicator", selector} }

// FocusTrap constrains keyboard focus (Tab/Shift+Tab) to elements
// within this container. Use this for modals and drawers to prevent
// focus from escaping to elements behind the overlay - required for
// accessibility.
func FocusTrap() Option { return Option{"tether-focus-trap", ""} }

// Timing options - control how frequently events reach the server.

// Debounce overrides the default input debounce delay for this element.
// The default (300ms, configurable via [tether.App].Client.DefaultDebounce)
// groups rapid keystrokes into a single event. Set a shorter duration
// for search-as-you-type, or a longer one for expensive operations.
func Debounce(d time.Duration) Option {
	return Option{"tether-debounce", strconv.Itoa(int(d.Milliseconds()))}
}

// Throttle sets a minimum interval between events from this element.
// Unlike [Debounce] which waits for a pause, Throttle fires the first
// event immediately and drops subsequent events until the interval
// elapses. Use this for scroll or resize handlers where you want
// regular updates without flooding the server.
func Throttle(d time.Duration) Option {
	return Option{"tether-throttle", strconv.Itoa(int(d.Milliseconds()))}
}

// FilterKey restricts an [OnKeyDown] binding to fire only for a specific
// key (e.g. "Enter", "Escape"). Other keys are silently ignored by the
// client and never reach the server.
func FilterKey(key string) Option { return Option{"tether-key", key} }

// Data sets a custom data-tether-* attribute on the element. This is
// the escape hatch for attributes not covered by the built-in options.
func Data(key, value string) Option { return Option{key, value} }

// EventData attaches a static key-value pair to every event from this
// element. The pair appears in [tether.Event].Data alongside any
// values the client collects automatically (input value, form fields).
// Use this to carry context - like an item ID - with each click so the
// handler knows which item was acted on without maintaining server-side
// selection state.
func EventData(key, value string) Option { return Option{"tether-data-" + key, value} }

// Directive options - client-side behaviour that runs entirely in the
// browser without a server round-trip.

// Link enables client-side navigation for anchor elements. Instead of
// a full page reload, the client intercepts the click, updates the
// browser URL via pushState, and sends a navigate event to the server.
// The server re-renders the active page and pushes a diff - only the
// changed content is updated, preserving scroll position and input
// state. Use this for navigation within a single tether handler. For
// links to a different handler (e.g. from /ws/ to /sse/), use a
// regular <a> tag so the browser performs a full page load.
func Link() Option { return Option{"tether-link", ""} }

// ToggleClass adds or removes a CSS class on click without a server
// round-trip. Useful for toggling visibility, active states, or themes
// entirely on the client. Combine with [ToggleTarget] to toggle a
// class on a different element.
func ToggleClass(class string) Option { return Option{"tether-toggle-class", class} }

// ToggleTarget directs a [ToggleClass] or [ToggleAttr] at a different
// element identified by a CSS selector, rather than the clicked element.
func ToggleTarget(selector string) Option { return Option{"tether-toggle-target", selector} }

// ToggleAttr toggles a boolean HTML attribute (e.g. "hidden", "disabled",
// "open") on click without a server round-trip.
func ToggleAttr(attr string) Option { return Option{"tether-toggle-attr", attr} }

// ScrollTo scrolls the matched element into view on click without a
// server round-trip. Uses smooth scrolling behaviour.
func ScrollTo(selector string) Option { return Option{"tether-scroll-to", selector} }

// PreserveScroll marks a scrollable container whose scroll position
// should survive DOM morphing. Without this, idiomorph may reset the
// scroll position when the container's content is updated. Use this
// on columns, chat feeds, or any scrollable region.
func PreserveScroll() Option { return Option{"tether-preserve-scroll", ""} }

// Cloak hides the element until the tether runtime initialises. The
// client removes the attribute on startup, making the element visible.
// Use this to prevent a flash of unbound content - e.g. a signal-bound
// element that would briefly show its raw template text before the
// signal value is applied.
func Cloak() Option { return Option{"tether-cloak", ""} }

// Permanent excludes the element from DOM morphing. When idiomorph
// processes a server update, it skips elements marked permanent  -
// their content, attributes, and children are preserved exactly as-is.
// Use this for elements with client-side state that must survive server
// updates (e.g. a video player, an interactive map, or a third-party
// widget that manages its own DOM).
func Permanent() Option { return Option{"tether-permanent", ""} }

// Signal binding options - bind DOM properties to server-pushed signals.
// Signals update elements directly on the client without a render cycle
// or diff. Push values via [tether.Session.Signal]; bound elements react
// instantly. Elements with signal bindings do not need Dynamic keys
// because they bypass the diff engine entirely.

// BindText replaces the element's text content with the signal's current
// value whenever the server pushes an update via [tether.Session.Signal].
// The signal name must match the key passed to Signal().
func BindText(signal string) Option { return Option{"tether-bind-text", signal} }

// BindShow makes the element visible when the named signal is truthy
// and hidden when falsy. Visibility is toggled via CSS display - the
// element remains in the DOM either way.
func BindShow(signal string) Option { return Option{"tether-bind-show", signal} }

// BindHide is the inverse of [BindShow]: the element is hidden when the
// signal is truthy and visible when falsy.
func BindHide(signal string) Option { return Option{"tether-bind-hide", signal} }

// BindClass adds the CSS class when the named signal is truthy and
// removes it when falsy. Use this for conditional styling that reacts
// to server-pushed state without a full render cycle.
func BindClass(class, signal string) Option {
	return Option{"tether-bind-class", class + " " + signal}
}

// BindAttr sets an HTML attribute to the signal's value. When the
// signal is falsy the attribute is removed entirely. Use this for
// dynamic attributes like "disabled", "aria-expanded", or "href".
func BindAttr(attr, signal string) Option {
	return Option{"tether-bind-attr", attr + " " + signal}
}

// BindValue binds a form element's value property (input, select,
// textarea) to a named signal. When the server pushes a new signal
// value, the form element's displayed value updates instantly.
func BindValue(signal string) Option { return Option{"tether-bind-value", signal} }

// Signal directive options - modify signal values on the client without
// waiting for a server response. These are purely client-side operations
// that update bound elements instantly.

// ToggleSignal flips a boolean signal between true and false on click.
// No server round-trip - the signal updates instantly on the client.
// Combine with [BindShow] or [BindClass] for UI that toggles without
// network latency (dropdowns, accordions, dark mode).
func ToggleSignal(signal string) Option { return Option{"tether-toggle-signal", signal} }

// SetSignal sets a signal to a specific value on click. No server
// round-trip - the signal updates instantly on the client. Use this
// for tab selection, radio-style patterns, or any case where clicking
// an element should set a known value.
func SetSignal(signal, value string) Option {
	return Option{"tether-set-signal", signal + " " + value}
}

// Optimistic sets a signal immediately on click AND sends the event to
// the server. The signal provides instant visual feedback while the
// server processes the event. If the server sends a different signal
// value in its response, the client updates to match - the server is
// always authoritative.
func Optimistic(signal, value string) Option {
	return Option{"tether-optimistic", signal + " " + value}
}

// OptimisticToggle flips a boolean signal immediately on click AND
// sends the event to the server. Like [Optimistic] but for boolean
// toggles - the signal flips instantly while the server processes the
// real state change.
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

// Upload options - file upload via the upload extension JS. Requires
// [tether.UploadConfig] on [tether.StatefulConfig].

// Upload marks the element as a file upload trigger. Clicking it opens
// the browser's file picker. When files are selected, they are uploaded
// as a multipart POST to the server and delivered to [tether.UploadConfig].Handle.
// The action string identifies which upload this is (e.g. "avatar",
// "document") and appears in [tether.Upload].Action.
func Upload(action string) Option { return Option{"tether-upload", action} }

// UploadInput provides a CSS selector for the file <input> element
// when it is not adjacent to the upload trigger in the DOM. By default
// the client looks for the nearest file input; use this when the input
// is elsewhere (e.g. hidden, in a different container).
func UploadInput(selector string) Option { return Option{"tether-upload-input", selector} }

// UploadProgress binds a <progress> element's value attribute to the
// upload progress for the named action. The client updates the value
// (0–100) as bytes are sent, giving users a visual progress bar.
func UploadProgress(action string) Option {
	return Option{"tether-bind-attr", "value upload:" + action + ":progress"}
}

// Push options - Web Push notification subscription. Requires
// [tether.PushConfig] on [tether.StatefulConfig].

// PushSubscribe marks a button for Web Push subscription. On click, the
// browser requests notification permission from the user and, if
// granted, subscribes via the service worker's PushManager. The
// subscription is sent to [tether.PushConfig].OnSubscribe so it can be
// stored for later use with [tether.Session.Push].
func PushSubscribe() Option { return Option{"tether-push-subscribe", ""} }

// Lifecycle options - integrate with the client JS lifecycle and CSS
// transition system.

// Hook attaches a named JS lifecycle hook to the element. Hooks are
// defined in application JS (e.g. hooks.js) and receive callbacks for
// mounted, updated, and destroyed events. Use this to integrate
// third-party JS libraries (charts, maps, editors) that need to
// initialise when the element appears, update when its data attributes
// change, and clean up when it is removed from the DOM.
func Hook(name string) Option { return Option{"tether-hook", name} }

// Transition enables CSS enter/leave transitions on the element. The
// name maps to CSS class prefixes: {name}-enter-from, {name}-enter-to,
// {name}-leave-from, {name}-leave-to. When the element is added to the
// DOM, the enter classes are applied; when removed, the leave classes
// animate the element out before it is actually deleted.
func Transition(name string) Option { return Option{"tether-transition", name} }

// Clipboard options - copy text to the clipboard without a server
// round-trip. Handled entirely on the client.

// CopyToClipboard copies the text content of the element matched by
// selector to the clipboard on click. No server round-trip - the copy
// happens entirely in the browser. Use this for "copy" buttons next
// to API keys, code snippets, or share URLs.
func CopyToClipboard(selector string) Option { return Option{"tether-copy", selector} }

// Keyboard shortcut options - global hotkeys that fire regardless of
// which element has focus.

// Hotkey registers a global keyboard shortcut. When the key combo is
// pressed anywhere on the page, the action fires as a normal tether
// event. The combo format is modifier keys joined with + followed by
// the key name: "ctrl+k", "escape", "shift+?", "ctrl+shift+p".
//
// Multiple hotkeys can be applied to the same element - each creates
// a unique data attribute:
//
//	bind.Apply(container,
//	    bind.Hotkey("ctrl+k", "search.open"),
//	    bind.Hotkey("escape", "modal.close"),
//	)
func Hotkey(combo, action string) Option {
	norm := strings.ReplaceAll(combo, "+", "-")
	return Option{"tether-hotkey-" + norm, action}
}

// Drag and drop options - mark elements as draggable or as drop
// targets. Handled by the tether-drag-and-drop.js extension which is
// auto-included when any element with data-tether-draggable appears
// in the initial page render.
//
// Extension scripts are loaded once during the initial GET. If the
// initial view does not contain draggable elements (e.g. a login
// page renders first), include a hidden marker so the script loads
// upfront:
//
//	bind.Apply(div.New().Class("sr-only"), bind.Draggable())

// Draggable marks an element as draggable. Pair with [EventData] to
// attach identifying data (e.g. an item ID) that travels with the
// drag:
//
//	bind.Apply(card,
//	    bind.Draggable(),
//	    bind.EventData("id", card.ID),
//	)
func Draggable() Option { return Option{"tether-draggable", ""} }

// DropTarget marks an element as a valid drop zone. When a draggable
// element is dropped onto this target, the action fires with event
// data merged from both the dragged element and the target. Pair with
// [EventData] to identify the target:
//
//	bind.Apply(column,
//	    bind.DropTarget("card.move"),
//	    bind.EventData("column", "1"),
//	)
func DropTarget(action string) Option { return Option{"tether-drop-target", action} }
