# Events

## Event types

Event type constants live in the `event` package. Import `github.com/jpl-au/fluent-tether/event`.

| Constant | Wire value | Trigger |
|----------|-----------|---------|
| `event.Click` | `"click"` | Button or element click |
| `event.Input` | `"input"` | Text input value change (debounced by default) |
| `event.Submit` | `"submit"` | Form submission — includes all named field values |
| `event.Change` | `"change"` | Select, checkbox, or radio commit |
| `event.KeyDown` | `"keydown"` | Key press — key name and modifiers in `ev.Data` |
| `event.Focus` | `"focus"` | Element receives focus |
| `event.Blur` | `"blur"` | Element loses focus |
| `event.Navigate` | `"navigate"` | Client-side navigation (pushState) |
| `event.Viewport` | `"viewport"` | Element enters the viewport — used for infinite scroll |
| `event.Online` | `"online"` | Browser connection restored |
| `event.Offline` | `"offline"` | Browser connection lost |
| `event.AppInstalled` | `"appinstalled"` | PWA installed to home screen |

Use `event.Custom("name")` to create constants for custom event names not covered above.

## Binding events

Every bind helper adds a `data-tether-*` attribute that the client JS picks up. The helper returns the same element type it received, so binding composes naturally:

```go
bind.Click(button.Text("Save"), "save")
bind.Submit(form.New(fields...), "register")
bind.Input(input.Text("q", ""), "search")
bind.Change(select.New(options...), "filter")
bind.KeyDown(input.Text("msg", ""), "send")
bind.FilterKey(input.Text("msg", ""), "Enter")  // restrict to Enter key only
bind.Focus(input.Text("name", ""), "focus")
bind.Blur(input.Text("name", ""), "blur")
bind.Viewport(div.New(), "load-more")          // fires when element enters viewport
bind.On(el, "dblclick", "open-editor")         // arbitrary DOM event
```

### Collecting input values on click

`bind.Collect` lets a button gather values from inputs elsewhere in the DOM at click time, without requiring a form wrapper:

```go
// The button collects the value of #search-input when clicked
bind.Collect(
    bind.Click(button.Text("Search"), "search"),
    "#search-input",
)
```

The selector is evaluated at click time with `document.querySelectorAll`. Elements are collected by `name` attribute first, then `id`. Checkbox and radio inputs send `"true"` or `"false"`; all other inputs send their current value.

Multiple selectors work the same way:

```go
bind.Collect(
    bind.Click(button.Text("Go"), "go"),
    "#query, #filter, #sort",
)
```

With `bind.Apply`:

```go
bind.Apply(button.Text("Search"),
    bind.OnClick("search"),
    bind.WithCollect("#search-input"),
)
```

## Timing

### Debounce

`bind.Input` is debounced at 300ms by default (configurable via `Config.Client.DefaultDebounce`). Override per element:

```go
bind.Debounce(bind.Input(input.Text("q", ""), "search"), 150*time.Millisecond)
// or
bind.Apply(input.Text("q", ""),
    bind.OnInput("search"),
    bind.WithDebounce(150*time.Millisecond),
)
```

### Throttle

Minimum interval between events — useful for scroll or resize handlers:

```go
bind.Throttle(bind.Viewport(div.New(), "scroll"), time.Second)
```

## Loading states

### Disable during request

Disable the element while its event is being processed:

```go
bind.Disable(bind.Click(button.Text("Save"), "save"), "Saving...")
```

The optional text argument replaces the button label during the request and is restored when the response arrives.

### Confirmation prompt

Show a browser `confirm()` dialog before sending the event:

```go
bind.Confirm(bind.Click(button.Text("Delete"), "delete"), "Are you sure?")
```

The event is only sent if the user confirms.

### Loading indicator

Show or hide a loading indicator at a CSS selector while the event is in flight:

```go
bind.Indicator(bind.Click(button.Text("Load"), "load"), "#spinner")
```

## Forms

`bind.Submit` sends all named field values as `ev.Data`:

```go
form.New(
    input.Text("email", "").Name("email"),
    input.Password("password").Name("password"),
    bind.Submit(button.Text("Login"), "login"),
)

// In Handle:
email := ev.Data["email"]
password := ev.Data["password"]
```

`bind.Reset` clears form fields after a successful submit:

```go
bind.Reset(bind.Submit(button.Text("Send"), "send"))
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

Attach static key-value pairs to any event — they arrive in `ev.Data`:

```go
bind.EventData(button.Text("Delete"), "id", itemID)
```

## Key filtering

Restrict a `keydown` binding to a specific key:

```go
bind.FilterKey(input.Text("msg", ""), "Enter")
```

The event is only sent when that key is pressed. Use `ev.Key()` in Handle to read the key name when not filtering.

## PWA and connectivity events

`event.Online`, `event.Offline`, and `event.AppInstalled` are fired automatically by the client JS when the corresponding browser events occur. No bind helper is needed — the events arrive as regular server events:

```go
Handle: func(_ tether.PreSession, s State, ev tether.Event) State {
    switch ev.Action {
    case "online":
        s.Status = "Connected"
    case "offline":
        s.Status = "Offline — changes will sync when reconnected"
    case "appinstalled":
        s.Installed = true
    }
    return s
},
```

`event.Viewport` fires when a bound element scrolls into the viewport — useful for infinite scroll and lazy loading:

```go
bind.Viewport(div.New().ID("sentinel"), "load-more")
```
