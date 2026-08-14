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
func OnClick(action string) Option { return Option{eventAttr + "click", action} }

// OnSubmit forwards form submissions to the server. All named fields
// inside the form are automatically collected into [tether.Event].Data
// so the handler can read them by name (ev.Data["email"], ev.Int("age")).
func OnSubmit(action string) Option { return Option{eventAttr + "submit", action} }

// OnInput forwards input events to the server with debouncing. The
// element's current value is included automatically in
// [tether.Event].Data["value"]. Debounce delay defaults to 300ms
// (configurable via [tether.App].Client.DefaultDebounce or per-element
// via [Debounce]).
func OnInput(action string) Option { return Option{eventAttr + "input", action} }

// OnChange forwards change events to the server. Unlike [OnInput], this
// fires once when the user commits a value (leaving a text field,
// selecting a dropdown option, toggling a checkbox). The element's value
// is included in [tether.Event].Data["value"].
func OnChange(action string) Option { return Option{eventAttr + "change", action} }

// OnKeyDown forwards keydown events to the server. The pressed key name
// (e.g. "Enter", "Escape", "ArrowUp") is available via
// [tether.Event].Key(). Combine with [FilterKey] to restrict which
// keys trigger the server event.
func OnKeyDown(action string) Option { return Option{eventAttr + "keydown", action} }

// OnFocus forwards focus events to the server. Fires when the element
// receives keyboard focus (click, tab, or programmatic focus).
func OnFocus(action string) Option { return Option{eventAttr + "focus", action} }

// OnBlur forwards blur events to the server. Fires when the element
// loses keyboard focus. Useful for validating input on exit.
func OnBlur(action string) Option { return Option{eventAttr + "blur", action} }

// OnPaste forwards paste events to the server. The pasted text is
// included in [tether.Event].Data["value"]. Use this for
// paste-to-search, paste-to-import, or paste-from-clipboard features.
func OnPaste(action string) Option { return Option{eventAttr + "paste", action} }

// OnViewport fires when the element enters the visible viewport, using
// an IntersectionObserver internally. Place this on a sentinel element
// at the bottom of a list to implement infinite scroll - when the
// sentinel scrolls into view, the server loads the next page of data.
func OnViewport(action string) Option { return Option{"tether-viewport", action} }

// eventAttr prefixes every server event binding. The prefix is the
// client's only signal that an attribute names a DOM event rather than
// a control (data-tether-disable, data-tether-at, ...), so it must stay
// in step with eventAttrPrefix in client/tether.js. No control
// attribute may begin with "event-".
const eventAttr = "tether-event-"

// On forwards a DOM event to the server. Any DOM event works: the
// client attaches a delegated listener for each event name it finds in
// the rendered HTML, at load and after every update, so nothing has to
// be registered up front.
//
//	bind.Apply(el, bind.On("dblclick", "open-editor"))
//	bind.Apply(card, bind.On("mouseenter", "card.preview"), bind.Delay(400*time.Millisecond))
//	bind.Apply(canvas, bind.On("wheel", "zoom"), bind.PreventDefault())
//
// The dedicated options ([OnClick], [OnInput], [OnSubmit] and friends)
// are the same mechanism with the event name filled in - reach for them
// first, and for On when the event you want has no shorthand.
//
// A binding fires where addEventListener on the same element would: an
// event that bubbles counts when it happens on a descendant, one that
// does not (focus, blur, scroll, mouseenter) only when the element
// itself is the target. Use "focusin" and "focusout" for the bubbling
// forms of focus and blur.
//
// Continuous events - mousemove, pointermove, touchmove, drag,
// dragover, scroll, wheel and resize - are coalesced to at most one
// event per animation frame, keeping the latest, so a binding on them
// cannot flood the transport. [Throttle], [Debounce] and [Delay]
// override that; per-event behaviour like [PreventDefault] still runs
// on every occurrence.
//
// The name travels in the attribute name, which HTML lowercases, so it
// must be lowercase and may contain only letters, digits and - _ . or
// : - every DOM event name qualifies, as do the usual custom-event
// conventions ("sl-change", "cart:updated"). A name outside that
// grammar cannot be carried at all, so it panics here rather than
// rendering an attribute the browser would never act on. To forward a
// mixed-case custom event, dispatch it from a [Hook] with
// Tether.sendEvent.
func On(name, action string) Option {
	if !bindableEvent(name) {
		panic("bind: On cannot bind event name " + strconv.Quote(name) +
			" - an event name travels in an attribute name, so it must be lowercase and contain only letters, digits and - _ . :")
	}
	return Option{eventAttr + name, action}
}

// bindableEvent reports whether name can be carried as the tail of the
// attribute name data-tether-event-<name>. The check is a whitelist
// rather than a blacklist because Fluent escapes attribute values but
// writes keys verbatim, so an unchecked name would be an attribute
// injection.
func bindableEvent(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.' || c == ':':
		default:
			return false
		}
	}
	return true
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
//	bind.Apply(el, bind.On("contextmenu", "menu.open"), bind.PreventDefault())
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
// Panics if d is negative.
func Debounce(d time.Duration) Option {
	if d < 0 {
		panic("bind: Debounce duration must not be negative")
	}
	return Option{"tether-debounce", strconv.Itoa(int(d.Milliseconds()))}
}

// DebounceLeading is a leading-edge debounce: it sends the event
// immediately on the first keystroke, then suppresses further events
// until the input has been quiet for d. [Debounce] is the opposite
// (trailing-edge) - it waits for the pause before sending. Reach for
// the leading edge when the first character should act at once (opening
// a suggestions panel, marking a field dirty) while the burst that
// follows is coalesced. Panics if d is negative.
func DebounceLeading(d time.Duration) Option {
	if d < 0 {
		panic("bind: DebounceLeading duration must not be negative")
	}
	return Option{"tether-debounce-leading", strconv.Itoa(int(d.Milliseconds()))}
}

// Throttle sets a minimum interval between events from this element.
// Unlike [Debounce] which waits for a pause, Throttle fires the first
// event immediately and drops subsequent events until the interval
// elapses. Use this for scroll or resize handlers where you want
// regular updates without flooding the server. Panics if d is negative.
func Throttle(d time.Duration) Option {
	if d < 0 {
		panic("bind: Throttle duration must not be negative")
	}
	return Option{"tether-throttle", strconv.Itoa(int(d.Milliseconds()))}
}

// FilterKey restricts an [OnKeyDown] binding to fire only for a specific
// key (e.g. "Enter", "Escape"). Other keys are silently ignored by the
// client and never reach the server. Renders as data-tether-filterkey,
// mirroring this option's name - it is unrelated to data-fluent-key,
// the diff engine's element identity.
func FilterKey(key string) Option { return Option{"tether-filterkey", key} }

// Event modifiers - change where an event binding listens or when it
// fires. Stack them with any event option ([OnClick], [OnKeyDown],
// [Event], ...) via [Apply]. They are declarative attributes handled by
// the client runtime, so they add no eval and stay CSP-safe.

// Outside fires the binding when the event happens OUTSIDE the element
// rather than on it. This is the click-outside primitive for dropdowns,
// popovers, and modals: put [OnClick] and Outside on the open panel and
// the action fires whenever the user clicks anywhere else. Outside takes
// priority over [Window] and [Document] when combined on one element.
//
//	// Close the menu when a click lands outside its panel.
//	bind.Apply(panel, bind.OnClick("menu.close"), bind.Outside())
func Outside() Option { return Option{"tether-outside", ""} }

// Once fires the binding at most once; every event after the first is
// ignored. A DOM morph that replaces the element resets this - the
// fresh element fires once again. Pair with [Outside] for a dismiss
// handler that runs a single time.
func Once() Option { return Option{"tether-once", ""} }

// Window listens for the event at the window level instead of on the
// element, so it fires no matter where the event occurs on the page.
// Use it for global keyboard handlers - an Escape that closes a modal
// regardless of focus - without the element having to hold focus.
// Combine with [FilterKey] to select a single key.
//
//	bind.Apply(modal, bind.OnKeyDown("modal.close"), bind.Window(), bind.FilterKey("Escape"))
func Window() Option { return Option{"tether-at", "window"} }

// Document listens for the event at the document level instead of on
// the element. Like [Window] but scoped to document, which is the right
// choice for delegated handlers that should ignore events dispatched
// directly on the window object.
func Document() Option { return Option{"tether-at", "document"} }

// Stop calls stopPropagation on the event so it does not bubble to
// ancestor handlers. Use it on a control inside a larger clickable
// region to keep the inner action from also triggering the outer one -
// for example a delete button inside a row that itself opens on click.
func Stop() Option { return Option{"tether-stop", ""} }

// Delay postpones sending the event to the server by d after it fires.
// Unlike [Debounce], which coalesces a burst, Delay always sends - just
// later. Use it to defer a hover-triggered load so a quick pass-through
// does not fire, or to stagger an action behind an animation. Panics if
// d is negative.
func Delay(d time.Duration) Option {
	if d < 0 {
		panic("bind: Delay duration must not be negative")
	}
	return Option{"tether-delay", strconv.Itoa(int(d.Milliseconds()))}
}

// Data sets a custom data-tether-* attribute on the element. This is
// the escape hatch for attributes not covered by the built-in options.
func Data(key, value string) Option { return Option{key, value} }

// EventData attaches a key-value pair to every event from this element.
// The pair appears in [tether.Event].Data alongside any values the
// client collects automatically (input value, form fields). Use this to
// carry context - like an item ID - with each click so the handler knows
// which item was acted on without maintaining server-side selection
// state.
//
// The value is rendered into a data attribute and only refreshes when
// the element itself is re-rendered. The diff engine re-renders a region
// only when it carries a [node.Dynamic] key (this is true in every mode,
// including the plain differ), so a value that CHANGES between renders
// must live inside the Dynamic region that updates - otherwise it is
// frozen at its first-render value while the visible content around it
// updates, a silent mismatch. A value that never changes for a given
// element (the intended use - a stable item ID) can sit anywhere.
//
//	// Wrong: count changes but the button is outside any Dynamic key,
//	// so data-tether-data-n stays "0" forever.
//	bind.Apply(button.Text("+"), bind.OnClick("inc"), bind.EventData("n", n))
//
//	// Right: the button lives in the region keyed to the count, so its
//	// event data refreshes with each update.
//	div.New(
//	    bind.Apply(button.Text("+"), bind.OnClick("inc"), bind.EventData("n", n)),
//	).Dynamic("counter")
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

// AutoScroll marks a scrollable container that should automatically
// scroll to the bottom after each morph. Use this on log viewers,
// streaming output, and chat feeds where new content appears at the
// bottom and the user should follow along. Unlike [PreserveScroll]
// which maintains the current position, AutoScroll always moves to
// the latest content.
func AutoScroll() Option { return Option{"tether-auto-scroll", ""} }

// Cloak hides the element until the tether runtime initialises. The
// client removes the attribute on startup, making the element visible.
// Use this to prevent a flash of unbound content - e.g. a signal-bound
// element that would briefly show its raw template text before the
// signal value is applied.
func Cloak() Option { return Option{"tether-cloak", ""} }

// Permanent excludes the element from DOM morphing. When idiomorph
// processes a server update, it skips elements marked permanent -
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

// Text replaces the element's text content with the signal's current
// value whenever the server pushes an update via [tether.Session.Signal].
// The signal name must match the key passed to Signal().
func Text(signal string) Option { return Option{"tether-bind-text", signal} }

// Show makes the element visible when the named signal is truthy
// and hidden when falsy. Visibility is toggled via CSS display - the
// element remains in the DOM either way.
func Show(signal string) Option { return Option{"tether-bind-show", signal} }

// Hide is the inverse of [Show]: the element is hidden when the
// signal is truthy and visible when falsy.
func Hide(signal string) Option { return Option{"tether-bind-hide", signal} }

// Class adds the CSS class when the named signal is truthy and
// removes it when falsy. Use this for conditional styling that reacts
// to server-pushed state without a full render cycle.
func Class(class, signal string) Option {
	return Option{"tether-bind-class", class + " " + signal}
}

// Attr sets an HTML attribute to the signal's value. When the
// signal is falsy the attribute is removed entirely. Use this for
// dynamic attributes like "disabled", "aria-expanded", or "href".
func Attr(attr, signal string) Option {
	return Option{"tether-bind-attr", attr + " " + signal}
}

// Value binds a form element's value property (input, select,
// textarea) to a named signal. When the server pushes a new signal
// value, the form element's displayed value updates instantly.
func Value(signal string) Option { return Option{"tether-bind-value", signal} }

// Signal directive options - modify signal values on the client without
// waiting for a server response. These are purely client-side operations
// that update bound elements instantly.

// ToggleSignal flips a boolean signal between true and false on click.
// No server round-trip - the signal updates instantly on the client.
// Combine with [Show] or [Class] for UI that toggles without
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
//	bind.Apply(button.Text("Send"),
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

// Transition enables CSS enter/leave transitions on the element. When
// the element is added to the DOM the client adds tether-{name}-enter
// before insertion and removes it on the next frame, so a CSS transition
// runs from the enter state to the resting state. When the element is
// removed the client adds tether-{name}-leave and waits for transitionend
// (or Client.TransitionTimeout, default 5s) before deleting the node.
// Define both classes in your stylesheet, e.g. .tether-fade-enter and
// .tether-fade-leave for Transition("fade").
func Transition(name string) Option { return Option{"tether-transition", name} }

// Clipboard options - copy text to the clipboard without a server
// round-trip. Handled entirely on the client.

// CopyToClipboard copies the text content of the element matched by
// selector to the clipboard on click. No server round-trip - the copy
// happens entirely in the browser. Use this for "copy" buttons next
// to API keys, code snippets, or share URLs.
func CopyToClipboard(selector string) Option { return Option{"tether-copy", selector} }

// Client-side action feedback - temporary visual response after a
// client-only action (clipboard copy, scroll-to, etc.) without a
// server round-trip.

// FlashText temporarily replaces the element's text content after a
// client-side action succeeds (e.g. clipboard copy). The original
// text is restored after the configured flash duration
// (Client.FlashDuration, default 5s). No server round-trip. Pair
// with [CopyToClipboard] or other client-side actions:
//
//	bind.Apply(btn,
//	    bind.CopyToClipboard("#key"),
//	    bind.FlashText("Copied!"),
//	)
func FlashText(text string) Option { return Option{"tether-flash-text", text} }

// FlashClass temporarily adds a CSS class to the element after a
// client-side action succeeds. The class is removed after the
// configured flash duration. Use this for richer feedback like
// colour changes, icon swaps, or animations:
//
//	bind.Apply(btn,
//	    bind.CopyToClipboard("#key"),
//	    bind.FlashClass("copied"),
//	)
func FlashClass(class string) Option { return Option{"tether-flash-class", class} }

// Keyboard shortcut options - global hotkeys that fire regardless of
// which element has focus.

// Hotkey registers a global keyboard shortcut. When the key combo is
// pressed anywhere on the page, the action fires as a normal tether
// event. The combo format is modifier keys joined with + followed by
// the key name: "ctrl+k", "escape", "shift+?", "ctrl+shift+p".
//
// Modifiers are ctrl, meta, shift, and alt - ctrl and meta (Cmd on
// macOS, Win key on Windows) are distinct, so a ctrl combo never
// swallows Cmd+C. Use the "mod" alias for the platform's primary
// command modifier (meta on macOS, ctrl elsewhere) when one shortcut
// should feel native everywhere: "mod+k". Modifiers must appear in
// the order ctrl, meta, shift, alt (or mod, shift, alt).
//
// Hotkeys without ctrl/meta/alt do not fire while an input, textarea,
// select, or contenteditable element has focus - typing "/" into a
// search box is text, not a shortcut.
//
// One hotkey per element. For multiple hotkeys, apply each to its
// own element:
//
//	bind.Apply(div.New(), bind.Hotkey("mod+k", "search.open"))
//	bind.Apply(div.New(), bind.Hotkey("escape", "modal.close"))
//
// The client runtime builds a registry from [data-tether-hotkey]
// elements on init and after each morph. Lookups are O(1) per
// keypress with no CSS selector queries.
func Hotkey(combo, action string) Option {
	norm := strings.ToLower(strings.ReplaceAll(combo, "+", "-"))
	return Option{"tether-hotkey", norm + " " + action}
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

// Sortable marks a container for within-container reordering. When a
// draggable element is dropped inside a sortable container, the
// action fires with the drop index in event data ("index"). This
// enables priority reordering within a list or column. Sortable
// containers are also valid drop targets - items can be dragged in
// from outside.
//
//	bind.Apply(todoColumn,
//	    bind.Sortable("card.reorder"),
//	    bind.EventData("column", "0"),
//	)
func Sortable(action string) Option { return Option{"tether-sortable", action} }

// Client-side validation options - suppress form submission when
// validation fails, showing an error without a server round-trip.
// The server-side Handle remains authoritative for complex rules.

// Required prevents form submission if the field is empty. The
// message is shown as a browser validation tooltip.
func Required(message string) Option { return Option{"tether-required", message} }

// MinLength prevents form submission if the field value is shorter
// than n characters. The message is shown as a validation tooltip.
// Panics if n is negative.
func MinLength(n int, message string) Option {
	if n < 0 {
		panic("bind: MinLength must not be negative")
	}
	return Option{"tether-minlength", strconv.Itoa(n) + " " + message}
}

// MaxLength prevents form submission if the field value exceeds n
// characters. The message is shown as a validation tooltip.
// Panics if n is negative.
func MaxLength(n int, message string) Option {
	if n < 0 {
		panic("bind: MaxLength must not be negative")
	}
	return Option{"tether-maxlength", strconv.Itoa(n) + " " + message}
}

// Pattern prevents form submission if the field value does not match
// the regular expression. The message is shown as a validation tooltip.
func Pattern(regex, message string) Option {
	return Option{"tether-pattern", regex + " " + message}
}

// Content editable options - forward inline-edited text to the server.

// Editable marks a contenteditable element and forwards its text
// content to the server when the element loses focus (blur). The
// action receives the edited text in ev.Value().
//
//	bind.Apply(span.Text("Click to edit").Attr("contenteditable", "true"),
//	    bind.Editable("item.rename"),
//	)
func Editable(action string) Option { return Option{"tether-editable", action} }

// Multi-select options - click, ctrl+click, and shift+click
// selection of items within a container.

// Selectable marks a container for multi-select. Children with
// [EventData] "id" attributes become selectable items. Click selects
// one (deselects others), Ctrl/Cmd+click toggles, Shift+click
// selects a range. Selected items receive the "tether-selected" CSS
// class. Selection is purely client-side - no server round-trip per
// click. Use [CollectSelected] on an action button to gather the
// selected IDs when needed.
func Selectable() Option { return Option{"tether-selectable", ""} }

// CollectSelected gathers the IDs of all selected items (those with
// the "tether-selected" class) within the container matched by
// selector. The IDs are included in the event data as a
// comma-separated "selected" key. Pair with [OnClick] on an action
// button:
//
//	bind.Apply(button.Text("Delete Selected"),
//	    bind.OnClick("items.delete"),
//	    bind.CollectSelected("#item-list"),
//	)
//
//	// In Handle:
//	ids := strings.Split(ev.Data["selected"], ",")
func CollectSelected(selector string) Option { return Option{"tether-collect-selected", selector} }

// Touch gesture options - handled by the tether-touch.js extension
// which is auto-included when any element renders data-tether-swipe
// or data-tether-longpress.

// OnSwipe fires when the user swipes on the element. The swipe
// direction is included in ev.Data["direction"] as "left", "right",
// "up", or "down". Only fires on touch devices.
func OnSwipe(action string) Option { return Option{"tether-swipe", action} }

// OnLongPress fires after a sustained touch (~500ms) on the element.
// Cancelled if the finger moves before the timeout. Common mobile
// alternative to right-click. Only fires on touch devices.
func OnLongPress(action string) Option { return Option{"tether-longpress", action} }

// Conditional signal bindings - like [Show] and [Class], but
// driven by a comparison against the signal value rather than plain
// truthiness. The server pushes one value (a count, a status string)
// and the client derives several booleans from it, so no extra boolean
// signals need to be pushed for conditions the client can compute
// itself. Handled entirely on the client, reacting the instant the
// signal changes.

// validConditionOps lists the comparison operators accepted by the
// conditional bindings. Kept here so every helper validates identically.
var validConditionOps = map[string]bool{
	">": true, ">=": true, "<": true, "<=": true, "==": true, "!=": true,
}

// checkOp panics on an unknown operator so a typo fails at construction
// rather than silently never matching on the client.
func checkOp(op string) {
	if !validConditionOps[op] {
		panic("bind: unknown comparison operator " + strconv.Quote(op) +
			` (want one of >, >=, <, <=, ==, !=)`)
	}
}

// ShowWhen shows the element when the named signal compares true
// against value using op, and hides it otherwise. op is one of ">",
// ">=", "<", "<=", "==", "!=". Numeric operands compare numerically;
// "==" and "!=" also compare strings and booleans. Panics on an unknown
// operator.
//
// ShowWhen is sugar over [Computed]'s engine: it compiles to the same
// postfix program the client VM runs for computed signals, so there is a
// single evaluator on the client rather than a separate comparison path.
//
//	// Visible only once the count passes five.
//	bind.Apply(warning, bind.ShowWhen("count", ">", 5))
func ShowWhen(signal, op string, value any) Option {
	checkOp(op)
	return Option{"tether-bind-show-when", comparisonProgram(signal, op, value)}
}

// HideWhen is the inverse of [ShowWhen]: the element is hidden
// when the comparison is true and visible otherwise. Panics on an
// unknown operator.
func HideWhen(signal, op string, value any) Option {
	checkOp(op)
	return Option{"tether-bind-hide-when", comparisonProgram(signal, op, value)}
}

// ClassWhen adds the CSS class while the named signal compares true
// against value using op, and removes it otherwise. Panics on an unknown
// operator.
//
//	// Turn the countdown red in the final ten seconds.
//	bind.Apply(timer, bind.ClassWhen("danger", "seconds", "<", 10))
func ClassWhen(class, signal, op string, value any) Option {
	checkOp(op)
	return Option{"tether-bind-class-when", class + "|" + comparisonProgram(signal, op, value)}
}

// Client-side events - let one element trigger a client-side action on
// another without a server round-trip. The event model everywhere else
// is client to server to client; this is the escape hatch for the cases
// where the server does not need to know: clearing a search box, closing
// a sibling dropdown, resetting a filter.

// Emit dispatches a named client event to every element matching
// selector when this element is clicked. Pair with [OnClientEvent] on
// the receiving elements. No server round-trip.
//
//	bind.Apply(clearBtn, bind.OnClick("noop"), bind.Emit("clear", "#search"))
func Emit(event, selector string) Option {
	return Option{"tether-emit", event + " " + selector}
}

// OnClientEvent runs a client-side signal action when this element
// receives the named client event from an [Emit]. The action is a
// [SetSignal] or [ToggleSignal] option - the same helpers used for
// click-driven signal actions - so the receiver reacts through the
// ordinary signal bindings ([Show], [Value], [Class]).
// No server round-trip. Panics if action is not a signal action.
//
//	// An input bound to the "query" signal clears when "clear" fires.
//	bind.Apply(input.New(),
//	    bind.Value("query"),
//	    bind.OnClientEvent("clear", bind.SetSignal("query", "")),
//	)
func OnClientEvent(event string, action Option) Option {
	verb, ok := strings.CutPrefix(action.key, "tether-")
	if !ok || (verb != "set-signal" && verb != "toggle-signal") {
		panic("bind: OnClientEvent action must be SetSignal or ToggleSignal")
	}
	return Option{"tether-on-" + event, verb + " " + action.value}
}

// Client-side filtering - connect a text input to a container of items
// and show or hide the items as the user types, matching against their
// text content. Entirely client-side: the server sends the full list
// once, the client narrows it with no round-trip per keystroke.

// Filter marks a text input as the filter control for the container
// matched by selector. As the user types, items in the container whose
// text does not contain the query (case-insensitive) are hidden. By
// default every direct child of the container is an item; mark a subset
// with [FilterItem] to filter only those.
//
//	bind.Apply(input.New(), bind.Filter("#item-list"))
func Filter(selector string) Option { return Option{"tether-filter", selector} }

// FilterItem marks an element as a filterable item within a [Filter]
// container. Use it when the items are not the container's direct
// children (e.g. each row wraps its content) so matching targets the
// right elements.
func FilterItem() Option { return Option{"tether-filter-item", ""} }

// Client-side templates - render a list on the client from a signal
// holding a JSON array, with no server round-trip per change. Handled by
// the tether-template.js extension, auto-included when any element
// renders data-tether-template.

// Template renders a client-side list into the container matched by
// target whenever the named signal changes. Apply it to a <template>
// element whose content is the markup for one item, using {{field}}
// placeholders for object fields (or {{.}} for a scalar array element).
// The server pushes the data as a JSON array via [tether.Session.Signal];
// the client stamps one clone per element. Interpolated values are
// HTML-escaped.
//
//	// <template> content: <li>{{name}} - {{email}}</li>
//	bind.Apply(itemTemplate, bind.Template("people", "#people-list"))
func Template(signal, target string) Option {
	return Option{"tether-template", signal + " " + target}
}
