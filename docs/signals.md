# Signals and reactivity

fluent-tether gives you three ways to update the UI. Use whichever fits your situation — or mix them freely.

## Server-driven rendering (the default)

The server renders HTML, diffs it against the previous tree, and sends patches or morphs to the client. This is the core model and works for everything:

```go
Handle: func(_ tether.Session, s State, ev tether.Event) State {
    s.Count++
    return s // the framework renders, diffs, and sends the update
},
```

Use this when the update involves structural changes, conditional rendering, or anything where the full render function should run.

## Signals — lightweight targeted updates

Signals let the server push individual values to the client without a full render cycle. Bound elements update instantly — no diff, no HTML.

```go
// Push a single value
sess.Signal("count", 42)

// Push several values at once
sess.Signals(map[string]any{
    "count":  42,
    "status": "online",
})
```

Bind elements to signals in your render function using the `bind` package:

```go
bind.BindText(span.New(), "count")                    // sets textContent
bind.BindShow(div.New(children...), "isOpen")          // shows when truthy, hides when falsy
bind.BindHide(div.New(children...), "isOpen")          // inverse of BindShow
bind.BindClass(span.New(), "active", "isSelected")     // toggles CSS class
bind.BindAttr(button.New(), "disabled", "isLoading")   // sets/removes attribute
bind.BindValue(input.Text("email", ""), "email")       // sets form field value
```

Signal bindings work **document-wide**, not just inside the tether root. This means navigation highlights, status indicators, and layout shell elements react instantly to signal pushes without triggering a full render.

After a morph replaces part of the DOM, the client reapplies current signal values to newly added elements automatically.

**When to use signals:** high-frequency updates where a full render cycle is wasteful — counters, progress bars, online status indicators, form field synchronisation.

## Client-side signal directives — instant feedback

Signal directives update signal values on the client without contacting the server at all. All signal bindings react instantly.

```go
// Toggle a boolean signal on click
bind.ToggleSignal(button.Text("Menu"), "menuOpen")

// Set a signal to a specific value on click (tab bars, radio selection)
bind.SetSignal(button.Text("Settings"), "tab", "settings")
```

The server can override any client-set signal at any time by calling `sess.Signal(key, correctValue)`.

## Optimistic updates — predict and correct

For predictable actions where the round-trip delay would feel sluggish, update a signal immediately before the event reaches the server. When the server responds, its signals overwrite the optimistic value — if the prediction was wrong, the DOM corrects itself.

```go
// Set a signal to a specific value immediately, then send the event
bind.Click(
    bind.Optimistic(button.Text("Like"), "liked", "true"),
    "like",
)

// Toggle a boolean signal immediately, then send the event
bind.Click(
    bind.OptimisticToggle(button.Text("Like"), "liked"),
    "like",
)
```

## Client-side directives — ephemeral state

For toggle-only UI (drawers, menus, modals) where the server doesn't need to know, use client-side directives. These are covered in [client-side features](client-side.md).

## Signal truthiness

`BindShow`, `BindHide`, `BindClass`, and `BindAttr` evaluate signal values using JavaScript truthiness rules. The following values are **falsy** (element hidden for BindShow, shown for BindHide):

| Value | Falsy? |
|-------|--------|
| `false` | Yes |
| `0` | Yes |
| `""` (empty string) | Yes |
| `nil` | Yes |

Everything else is **truthy**, including strings that look false:

| Value | Truthy? | Why |
|-------|---------|-----|
| `"false"` | Yes | Non-empty string |
| `"0"` | Yes | Non-empty string |
| `"no"` | Yes | Non-empty string |
| `42` | Yes | Non-zero number |
| `true` | Yes | Boolean true |

**Always use Go booleans and numbers for show/hide signals, not their string representations.** `Signal("flag", false)` hides the element; `Signal("flag", "false")` shows it because `"false"` is a non-empty string.

## Don't mix signals and state rendering on the same element

An element should be driven by **either** signals **or** state rendering — never both. If a signal updates an element's text and the next render cycle also touches that element, the render overwrites the signal value:

```go
// Wrong — the render and signal fight over the same element
bind.BindText(span.Textf("Count: %d", s.Count).Dynamic("count"), "count")

// Right — signal-only element, no Dynamic key needed
bind.BindText(span.New(), "count")

// Right — state-rendered element, no signal binding
span.Textf("Count: %d", s.Count).Dynamic("count")
```

Pick one update path per element. Use signals for high-frequency updates that bypass rendering. Use state rendering for everything else.

## When to use what

| Mode | Round-trip | Use case |
|------|-----------|----------|
| **Server rendering** | Yes | Structural changes, conditional rendering, list updates |
| **Signals** | Server → client only | Counters, status indicators, progress bars |
| **Signal directives** | None | Tab selection, menu state with server override |
| **Optimistic updates** | Yes (with instant preview) | Like buttons, checkboxes, toggles |
| **Client directives** | None | Drawer open/close, modal visibility |
