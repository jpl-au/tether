// Package bind provides element annotation helpers for tether.
// Each helper attaches a data-tether-* attribute to a Fluent element,
// telling the client JS runtime how to handle that element - which
// events to forward, what client-side behaviour to apply, or which
// reactive signals to bind.
//
// All bindings are applied via [Apply] with composable [Option] values:
//
//	bind.Apply(button.Text("Delete"),
//	    bind.OnClick("delete"),
//	    bind.Confirm("Are you sure?"),
//	    bind.Disable("Deleting..."),
//	)
//
// This top-to-bottom style scales cleanly as behaviours are stacked
// and provides a single, consistent way to annotate elements.
//
// Helpers validate their input at construction and panic on invalid
// values (an unknown operator, a negative duration, a malformed
// expression), so a mistake fails at render time with a positioned
// message rather than silently doing nothing in the browser.
//
// The option families, at a glance:
//
// # Sending events to the server
//
// [OnClick], [OnSubmit], [OnInput], [OnChange], [OnKeyDown], [OnFocus],
// [OnBlur], [OnPaste], [OnViewport], [OnSwipe], [OnLongPress],
// [Editable], [Hotkey], and [Event] for any other DOM event type.
//
// # Shaping when and how an event fires
//
// [Debounce], [DebounceLeading], [Throttle], [Delay], [Once],
// [Outside] (fire when the event happens outside the element, e.g.
// click-outside to dismiss), [Window], [Document], [Stop],
// [PreventDefault], [FilterKey], and [Confirm].
//
// # Attaching data to events
//
// [Data], [EventData], [Collect], [CollectSelected], and [Prefix] to
// namespace the actions of a whole subtree.
//
// # In-flight feedback
//
// [Disable], [Indicator] (reference-counted across overlapping
// requests), and [Reset].
//
// # Reactive signal bindings
//
// [Text], [Show], [Hide], [Class], [Attr], and [Value] bind an element
// to a signal; [ShowWhen], [HideWhen], and [ClassWhen] bind to a
// comparison; [Computed] declares a computed signal from an expression
// (arithmetic, comparisons, boolean logic, len - compiled in Go,
// evaluated client-side with no eval).
//
// # Client-side signal actions
//
// [SetSignal], [ToggleSignal], [Optimistic], [OptimisticToggle],
// [Emit], and [OnClientEvent] coordinate state between elements
// without a server round trip.
//
// # Client-only behaviours
//
// [ToggleClass], [ToggleTarget], [ToggleAttr], [Link], [Filter],
// [FilterItem], [Template], [CopyToClipboard], [FlashText],
// [FlashClass], [ScrollTo], [PreserveScroll], [AutoScroll], [Cloak],
// [Permanent], [Transition], [AutoFocus], [FocusTrap], [Draggable],
// [DropTarget], [Sortable], and [Selectable].
//
// # Client-side timers
//
// [Timer], [Countdown], [TimerPrecision], [TimerFormat], and
// [TimerOnComplete] run a stopwatch or countdown entirely in the
// browser, started and reset by server-pushed signals.
//
// # Form validation
//
// [Required], [MinLength], [MaxLength], and [Pattern] use the native
// constraint validation API.
//
// # Uploads, push, and lifecycle
//
// [Upload], [UploadInput], [UploadProgress], [PushSubscribe], and
// [Hook] for per-element JS lifecycle callbacks.
//
// The docs directory covers each family in depth: docs/events.md,
// docs/signals.md, and docs/client-side.md.
package bind

// Settable is the structural type constraint for element annotation
// helpers. Any Fluent element with a chainable SetData method satisfies
// it.
type Settable[E any] interface {
	SetData(string, string) E
}
