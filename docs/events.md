# Events

## Event types

Event type constants live in the `event` package. Import `github.com/jpl-au/tether/event`.

| Constant | Wire value | Trigger |
|----------|-----------|---------|
| `event.Click` | `"click"` | Button or element click |
| `event.Input` | `"input"` | Text input value change (debounced by default) |
| `event.Submit` | `"submit"` | Form submission - includes all named field values |
| `event.Change` | `"change"` | Select, checkbox, or radio commit |
| `event.KeyDown` | `"keydown"` | Key press - key name and modifiers in `ev.Data` |
| `event.Focus` | `"focus"` | Element receives focus |
| `event.Blur` | `"blur"` | Element loses focus |
| `event.Navigate` | `"navigate"` | Client-side navigation (pushState) |
| `event.Viewport` | `"viewport"` | Element enters the viewport - used for infinite scroll |
| `event.Paste` | `"paste"` | Paste from clipboard - pasted text in `ev.Data["value"]` |
| `event.Online` | `"online"` | Browser connection restored |
| `event.Offline` | `"offline"` | Browser connection lost |
| `event.AppInstalled` | `"appinstalled"` | PWA installed to home screen |

Use `event.Type("name")` to create constants for custom event names not covered above.

## Binding events

Use `bind.Apply` to attach event bindings to elements. Each option adds a `data-tether-*` attribute that the client JS picks up:

```go
bind.Apply(button.Text("Save"), bind.OnClick("save"))
bind.Apply(form.New(fields...), bind.OnSubmit("register"))
bind.Apply(input.Text("q", ""), bind.OnInput("search"))
bind.Apply(select.New(options...), bind.OnChange("filter"))
bind.Apply(input.Text("msg", ""), bind.OnKeyDown("send"))
bind.Apply(input.Text("msg", ""), bind.OnKeyDown("send"), bind.FilterKey("Enter"))
bind.Apply(input.Text("name", ""), bind.OnFocus("focus"))
bind.Apply(input.Text("name", ""), bind.OnBlur("blur"))
bind.Apply(div.New(), bind.OnViewport("load-more"))
bind.Apply(el, bind.On("dblclick", "open-editor"))
bind.Apply(input.Text("q", ""), bind.OnPaste("paste-search"))
```

### Any DOM event

The `On*` helpers above are shorthands. `bind.On` takes the event name
directly and works for **any** DOM event - there is no list to be on:

```go
bind.Apply(canvas, bind.On("wheel", "zoom"), bind.PreventDefault())
bind.Apply(card, bind.On("mouseenter", "card.preview"), bind.Delay(400*time.Millisecond))
bind.Apply(el, bind.On("sl-change", "sync"))   // custom events too
```

Each binding renders `data-tether-event-<name>`, and the client attaches
a delegated listener for every event name it finds in the page - on load
and after every update. Nothing is registered up front, so an event type
that first appears in a morph works exactly like one present at first
render.

A binding fires where `addEventListener` on the same element would: an
event that bubbles counts when it happens on a descendant, one that does
not (`focus`, `blur`, `scroll`, `mouseenter`) only when the element
itself is the target. Use `focusin`/`focusout` for the bubbling forms of
focus and blur.

**Continuous events are coalesced.** `mousemove`, `pointermove`,
`touchmove`, `drag`, `dragover`, `scroll`, `wheel` and `resize` send at
most one event per animation frame, keeping the latest sample - so the
server learns where the pointer *stopped*, not where it was when the
frame opened. `bind.Throttle`, `bind.Debounce` and `bind.Delay` override
this. Per-event behaviour such as `bind.PreventDefault` still runs on
every occurrence.

The event name travels in the attribute name, which HTML lowercases, so
it must be lowercase and contain only letters, digits and `-` `_` `.`
`:`. Anything else panics at construction rather than rendering an
attribute the browser would never act on - see
[bind-unbindable-event-name](errors.md#bind-unbindable-event-name).

### PreventDefault

By default only submit events get `preventDefault`. Use `bind.PreventDefault()` to suppress the browser's default behaviour for any event:

```go
bind.Apply(el,
    bind.On("contextmenu", "menu.open"),
    bind.PreventDefault(),
)
```

### Global hotkeys

`bind.Hotkey` registers keyboard shortcuts that fire regardless of which element has focus:

```go
bind.Apply(searchBox, bind.Hotkey("mod+k", "search.open"))
bind.Apply(modal, bind.Hotkey("escape", "modal.close"))
```

One hotkey per element - for several shortcuts, apply each to its own element. The combo format uses `+` between modifiers and the key: `"mod+k"`, `"ctrl+shift+p"`, `"escape"`. The event arrives in Handle with `ev.Data["combo"]` set to the normalised combo string.

Modifiers are `ctrl`, `meta` (Cmd on macOS, Win key on Windows), `shift`, and `alt` - ctrl and meta are distinct, so a ctrl combo never swallows Cmd+C on macOS. Use `mod` for the platform's primary command modifier (meta on macOS, ctrl elsewhere) when one shortcut should feel native everywhere. Hotkeys without ctrl/meta/alt do not fire while an input, textarea, select, or contenteditable element has focus - typing `/` into a search box is text, not a shortcut.

The hotkey runtime ships as an extension script (`tether-hotkey.js`) that the framework includes automatically whenever a rendered page uses `bind.Hotkey` - there is nothing to wire up.

### Client-side validation

Validation attributes suppress form submission when fields are invalid, using the browser's native constraint validation (tooltips, red outlines). The server-side Handle remains authoritative for complex rules.

```go
bind.Apply(input.Text("name", ""),
    bind.Required("Name is required"),
)
bind.Apply(input.Text("pw", ""),
    bind.MinLength(8, "At least 8 characters"),
)
bind.Apply(input.Text("bio", ""),
    bind.MaxLength(140, "Too long"),
)
bind.Apply(input.Text("email", ""),
    bind.Pattern("[^@]+@[^@]+", "Invalid email"),
)
```

### Content editable

`bind.Editable` marks an element for inline editing. The element gets `contenteditable="true"` and its text content is sent to the server on blur:

```go
bind.Apply(span.Text("Click to edit"),
    bind.Editable("item.rename"),
)

// In Handle:
case "item.rename":
    text := ev.Value() // the edited text
```

### Multi-select

`bind.Selectable` enables click, Ctrl+click, and Shift+click selection
on items within a container. Selection is purely client-side via the
`.tether-selected` CSS class - no server round-trip per click. Use
`bind.CollectSelected` on an action button to gather the selected IDs:

```go
// Container with selectable items:
bind.Apply(div.New(items...), bind.Selectable()).ID("item-list")

// Each item needs an id via EventData:
bind.Apply(div.New(span.Text(item.Name)), bind.EventData("id", item.ID))

// Action button collects selected IDs:
bind.Apply(button.Text("Delete Selected"),
    bind.OnClick("items.delete"),
    bind.CollectSelected("#item-list"),
)

// In Handle:
case "items.delete":
    ids := strings.Split(ev.Data["selected"], ",")
```

Selected items receive the `tether-selected` CSS class for styling.
Selection state survives DOM morphs via the client state preservation
mechanism.

### Collecting input values on click

`bind.Collect` lets a button gather values from inputs elsewhere in the DOM at click time, without requiring a form wrapper:

```go
bind.Apply(button.Text("Search"),
    bind.OnClick("search"),
    bind.Collect("#search-input"),
)
```

The selector is evaluated at click time with `document.querySelectorAll`. Elements are collected by `name` attribute first, then `id`. Checkbox and radio inputs send `"true"` or `"false"`; all other inputs send their current value.

Multiple selectors work the same way:

```go
bind.Apply(button.Text("Go"),
    bind.OnClick("go"),
    bind.Collect("#query, #filter, #sort"),
)
```

## Timing

### Debounce

`bind.OnInput` is debounced at 300ms by default (configurable via `App.Client.DefaultDebounce`). Override per element:

```go
bind.Apply(input.Text("q", ""),
    bind.OnInput("search"),
    bind.Debounce(150*time.Millisecond),
)
```

### Throttle

Minimum interval between events - useful for scroll or resize handlers:

```go
bind.Apply(div.New(),
    bind.OnViewport("scroll"),
    bind.Throttle(time.Second),
)
```

### Leading-edge debounce

`bind.Debounce` is trailing-edge: it waits for the pause, then sends the
final value. `bind.DebounceLeading` is the opposite - it sends on the
first keystroke and then suppresses further events until the input has
been quiet for the interval. Use it when the first character should act
immediately (open a suggestions panel, mark a field dirty) while the
burst that follows is coalesced:

```go
bind.Apply(input.Text("q", ""),
    bind.OnInput("search"),
    bind.DebounceLeading(300*time.Millisecond),
)
```

## Event modifiers

Modifiers change *where* an event binding listens or *when* it fires.
Stack them with any event option via `bind.Apply`. Like every binding
they are declarative `data-tether-*` attributes handled by the client
runtime - no eval, CSP-safe.

### Outside (click-outside)

`bind.Outside` fires the binding when the event happens **outside** the
element rather than on it - the click-outside primitive for dropdowns,
popovers, and modals. Put it on the open panel and the action fires
whenever the user clicks anywhere else:

```go
bind.Apply(menuPanel,
    bind.OnClick("menu.close"),
    bind.Outside(),
)
```

`Outside` takes priority over `Window`/`Document` when combined on one
element.

### Once

`bind.Once` fires the binding at most once; every event after the first
is ignored. A DOM morph that replaces the element resets the guard - the
fresh element fires once again.

```go
bind.Apply(banner, bind.OnClick("promo.dismiss"), bind.Once())
```

### Window / Document scope

`bind.Window` and `bind.Document` listen at the window or document level
instead of on the element, so the binding fires no matter where the
event occurs on the page. Use `Window` for a global keyboard handler -
an Escape that closes a modal regardless of focus - without the element
having to hold focus. Combine with `bind.FilterKey` to select one key:

```go
bind.Apply(modal,
    bind.OnKeyDown("modal.close"),
    bind.Window(),
    bind.FilterKey("Escape"),
)
```

`Document` is the same but scoped to `document`, for delegated handlers
that should ignore events dispatched directly on the window object.

### Stop propagation

`bind.Stop` calls `stopPropagation` on the event so it does not bubble
to ancestor handlers. Use it on a control inside a larger clickable
region to keep the inner action from also triggering the outer one:

```go
bind.Apply(deleteBtn,
    bind.OnClick("row.delete"),
    bind.Stop(),
    bind.EventData("id", rowID),
)
```

### Delay

`bind.Delay` postpones sending the event by a fixed interval after it
fires. Unlike `Debounce`, which coalesces a burst, `Delay` always sends
- just later. Use it to defer a hover-triggered load so a quick pass
does not fire:

```go
bind.Apply(card,
    bind.On("mouseenter", "card.preview"),
    bind.Delay(400*time.Millisecond),
)
```

## Loading states

### Disable during request

Disable the element while its event is being processed:

```go
bind.Apply(button.Text("Save"),
    bind.OnClick("save"),
    bind.Disable("Saving..."),
)
```

The optional text argument replaces the button label during the request and is restored when the response arrives.

### Confirmation prompt

Show a browser `confirm()` dialog before sending the event:

```go
bind.Apply(button.Text("Delete"),
    bind.OnClick("delete"),
    bind.Confirm("Are you sure?"),
)
```

The event is only sent if the user confirms.

### Loading indicator

Show or hide a loading indicator at a CSS selector while the event is in flight:

```go
bind.Apply(button.Text("Load"),
    bind.OnClick("load"),
    bind.Indicator("#spinner"),
)
```

## Forms

`bind.OnSubmit` sends all named field values as `ev.Data`:

```go
bind.Apply(form.New(
    input.Text("email", "").Name("email"),
    input.Password("password").Name("password"),
    button.Text("Login"),
), bind.OnSubmit("login"))

// In Handle:
email := ev.Data["email"]
password := ev.Data["password"]
```

`bind.Reset` clears form fields after a successful submit:

```go
bind.Apply(form.New(fields...),
    bind.OnSubmit("send"),
    bind.Reset(),
)
```

### Typed data extraction

```go
count, _ := ev.Int("count")
price, _ := ev.Float64("price")
if ev.Bool("confirmed") { ... }

// Struct binding
var form struct {
    Email string `tether:"email"`
    Age   int    `tether:"age"`
}
ev.Bind(&form)
```

## Extra data

Attach static key-value pairs to any event - they arrive in `ev.Data`:

```go
bind.Apply(button.Text("Delete"),
    bind.OnClick("delete"),
    bind.EventData("id", itemID),
)
```

## Key filtering

Restrict a `keydown` binding to a specific key:

```go
bind.Apply(input.Text("msg", ""),
    bind.OnKeyDown("send"),
    bind.FilterKey("Enter"),
)
```

The event is only sent when that key is pressed. Use `ev.Key()` in Handle to read the key name when not filtering.

## PWA and connectivity events

`event.Online`, `event.Offline`, and `event.AppInstalled` are fired automatically by the client JS when the corresponding browser events occur. No bind helper is needed - the events arrive as regular server events:

```go
Handle: func(_ tether.Session, s State, ev tether.Event) State {
    switch ev.Action {
    case "online":
        s.Status = "Connected"
    case "offline":
        s.Status = "Offline - changes will sync when reconnected"
    case "appinstalled":
        s.Installed = true
    }
    return s
},
```

`event.Viewport` fires when a bound element scrolls into the viewport - useful for infinite scroll and lazy loading:

```go
bind.Apply(div.New().ID("sentinel"), bind.OnViewport("load-more"))
```

## Touch gestures

`bind.OnSwipe` and `bind.OnLongPress` bind swipe and long-press gestures on touch devices. See [extensions](extensions.md#touch-gestures) for details and parameters.

## Component event routing

When using `StatefulConfig.Components`, events are dispatched by prefix before reaching the page's `Handle`. An event with action `"likes.increment"` is routed to the component mounted at prefix `"likes"` - the component receives the event with action `"increment"` (prefix stripped).

### Event.Target

When `StatefulConfig.Components` dispatches an event, `Event.Target` is set to the mount's prefix (e.g. `"likes"`). This lets middleware and logging identify which component handled the event:

```go
func loggingMiddleware[S any](next tether.HandleFunc[S]) tether.HandleFunc[S] {
    return func(sess tether.Session, s S, ev tether.Event) S {
        if ev.Target != "" {
            slog.Info("component event", "target", ev.Target, "action", ev.Action)
        }
        return next(sess, s, ev)
    }
}
```

### Event.WithAction

Returns a copy of the event with a different `Action`. Used internally by the mount system and `Route`/`RouteTyped` to strip prefixes. Available for custom dispatchers that need to rewrite actions before forwarding:

```go
stripped := ev.WithAction(strings.TrimPrefix(ev.Action, "prefix."))
```

---

[← Back to documentation](../README.md#documentation)
