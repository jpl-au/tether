# Events and bindings

## Event

```go
type Event struct {
    Type    event.Type        // Click, Input, Submit, Change, KeyDown, etc.
    Action  string            // application-defined action name
    Data    map[string]string // event-specific key-value pairs
    EventID string            // monotonic counter for correlation
    Target  string            // set by StatefulConfig.Components to the mount prefix
}

// Accessors
func (e Event) Value() string                      // e.Data["value"]
func (e Event) Key() string                        // keydown key name
func (e Event) Get(key string) (string, bool)      // check and get
func (e Event) Int(key string) (int, error)        // parse integer
func (e Event) Float64(key string) (float64, error) // parse float
func (e Event) Bool(key string) bool               // "true" → true
func (e Event) Bind(dest any) error                // struct-tag form binding
func (e Event) WithAction(action string) Event     // copy with different Action
```

Event type constants live in the `event` package: `event.Click`, `event.Input`, `event.Submit`, `event.Change`, `event.KeyDown`, `event.Focus`, `event.Blur`, `event.Navigate`, `event.Viewport`, `event.Online`, `event.Offline`, `event.AppInstalled`. Create custom types with `event.Custom("name")`.

### Typed data extraction

```go
count, _ := ev.Int("count")
if ev.Bool("confirmed") { ... }

var form struct {
    Email string `tether:"email"`
    Age   int    `tether:"age"`
}
ev.Bind(&form)
```

### Params

```go
type Params struct {
    Path  string     // URL path
    Query url.Values // parsed query parameters
}
```

Params provides typed extraction helpers that mirror `Event`'s API for
consistency - one data extraction pattern across the framework.

**Single-value helpers** (return error on missing/invalid):

```go
p.Get("q")              // string - first value for key
p.Int("page")           // (int, error)
p.Float64("min")        // (float64, error)
p.Bool("active")        // true only for "true"
```

**Soft getters** (return default on missing/invalid - ideal for optional
URL parameters):

```go
p.IntDefault("page", 1)      // int - returns 1 if missing or invalid
p.Float64Default("min", 0.0) // float64
p.BoolDefault("drafts", false) // bool - returns default if key absent
```

**Multi-value helpers** (for repeated keys like `?tag=go&tag=web`):

```go
p.Strings("tag")        // []string - all values for key
p.Ints("id")            // ([]int, error)
p.Float64s("v")         // ([]float64, error)
```

---

## Bind helpers

All helpers live in the `bind` package. Use `bind.Apply` to attach behaviours to any Fluent element:

```go
bind.Apply(btn,
    bind.OnClick("delete"),
    bind.Confirm("Sure?"),
    bind.Disable("Deleting..."),
)
```

### Server events

```go
bind.OnClick("act")         bind.OnSubmit("act")
bind.OnInput("act")         bind.OnChange("act")
bind.OnKeyDown("act")       bind.OnFocus("act")
bind.OnBlur("act")          bind.OnViewport("act")
bind.OnPaste("act")
bind.Event("dblclick", "act")
bind.Collect("#selector")
```

### Timing

```go
bind.Debounce(150*time.Millisecond)
bind.Throttle(time.Second)
bind.FilterKey("Enter")
bind.EventData("key", "val")
```

### Control

```go
bind.Disable("...")     bind.Confirm("...")
bind.Reset()            bind.AutoFocus()
bind.Indicator("#el")   bind.FocusTrap()
bind.PreventDefault()
bind.Required("msg")    bind.MinLength(8, "msg")
bind.MaxLength(140, "msg") bind.Pattern("regex", "msg")
```

### Signal bindings

```go
bind.BindText("count")           bind.BindShow("isOpen")
bind.BindHide("isOpen")          bind.BindClass("active", "sel")
bind.BindAttr("disabled", "busy") bind.BindValue("email")
```

### Client directives

```go
bind.Link()             bind.Cloak()
bind.Permanent()        bind.ToggleClass("cls")
bind.ToggleTarget("#x") bind.ToggleAttr("hidden")
bind.CopyToClipboard("#selector")
bind.ScrollTo("#selector")
bind.PreserveScroll()
bind.AutoScroll()
bind.Editable("action")
bind.Selectable()
bind.CollectSelected("#list")
bind.OnSwipe("action")
bind.OnLongPress("action")
```

### Signal directives

```go
bind.ToggleSignal("menuOpen")
bind.SetSignal("tab", "settings")
bind.Optimistic("liked", "true")
bind.OptimisticToggle("liked")
```

### Uploads

```go
bind.Upload("avatar")
bind.UploadInput("#avatar-input")
bind.UploadProgress("avatar")
bind.PushSubscribe()
```

### Keyboard shortcuts

```go
bind.Hotkey("ctrl+k", "search.open")
bind.Hotkey("escape", "modal.close")
```

Global hotkeys that fire regardless of which element has focus. The combo
format uses `+` between modifiers and the key name. Multiple hotkeys can
be applied to the same element.

### Drag and drop

```go
bind.Draggable()             // mark as draggable
bind.DropTarget("card.move") // mark drop zone
bind.Sortable("card.reorder") // reorderable container
```

`Draggable` marks an element for dragging. Pair with `bind.EventData` to
carry identifying data. `DropTarget` fires the action on drop with merged
data from both the source and target. `Sortable` is like DropTarget but
also calculates the drop index for within-container reordering.

The `tether-drag-and-drop.js` extension is auto-included when any element
renders `data-tether-draggable` or `data-tether-sortable`.

### Client-side timers

```go
bind.Timer("elapsed")                       // count-up timer, 1s precision, auto format
bind.Countdown(30*time.Second)              // count down from duration
bind.TimerPrecision(100*time.Millisecond)   // tick interval
bind.TimerFormat("mm:ss.S")                 // explicit display format
bind.TimerOnComplete("quiz.expired")        // event on countdown completion
```

Server controls via signals: `sess.Signal("name.running", true/false)` to
start/pause, `sess.Signal("name", 0)` to reset. The element's text content
is automatically bound to the formatted value. See
[client-side timers](client-side.md#timers) for details.

### Lifecycle

```go
bind.Hook("chart")      bind.Transition("fade")
```

### Custom attributes

```go
bind.Data("tether-custom", "value")  // data-tether-custom="value"
```

---

[← API reference](api.md)
