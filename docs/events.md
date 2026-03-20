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
| `event.Online` | `"online"` | Browser connection restored |
| `event.Offline` | `"offline"` | Browser connection lost |
| `event.AppInstalled` | `"appinstalled"` | PWA installed to home screen |

Use `event.Custom("name")` to create constants for custom event names not covered above.

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
bind.Apply(el, bind.Event("dblclick", "open-editor"))
```

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
