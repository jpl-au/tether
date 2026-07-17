# Signals and reactivity

Tether gives you three ways to update the UI. Use whichever fits your situation - or mix them freely.

## Server-driven rendering (the default)

The server renders HTML, diffs it against the previous tree, and sends patches or morphs to the client. This is the core model and works for everything:

```go
Handle: func(_ tether.Session, s State, ev tether.Event) State {
    s.Count++
    return s // the framework renders, diffs, and sends the update
},
```

Use this when the update involves structural changes, conditional rendering, or anything where the full render function should run.

## Signals - lightweight targeted updates

Signals let the server push individual values to the client without a full render cycle. Bound elements update instantly - no diff, no HTML.

```go
// Push a single value
sess.Signal("count", 42)

// Push several values at once
sess.Signals(map[string]any{
    "count":  42,
    "status": "online",
})
```

Bind elements to signals in your render function:

```go
bind.Apply(span.New(), bind.Text("count"))                    // sets textContent
bind.Apply(div.New(children...), bind.Show("isOpen"))          // shows when truthy
bind.Apply(div.New(children...), bind.Hide("isOpen"))          // inverse of Show
bind.Apply(span.New(), bind.Class("active", "isSelected"))     // toggles CSS class
bind.Apply(button.New(), bind.Attr("disabled", "isLoading"))   // sets/removes attribute
bind.Apply(input.Text("email", ""), bind.Value("email"))       // sets form field value
```

Signal bindings work **document-wide**, not just inside the tether root. This means navigation highlights, status indicators, and layout shell elements react instantly to signal pushes without triggering a full render.

After a morph replaces part of the DOM, the client reapplies current signal values to newly added elements automatically.

**When to use signals:** high-frequency updates where a full render cycle is wasteful - counters, progress bars, online status indicators, form field synchronisation.

## Client-side signal actions - instant feedback

Signal actions update signal values on the client without contacting the server at all. All signal bindings react instantly.

```go
// Toggle a boolean signal on click
bind.Apply(button.Text("Menu"), bind.ToggleSignal("menuOpen"))

// Set a signal to a specific value on click (tab bars, radio selection)
bind.Apply(button.Text("Settings"), bind.SetSignal("tab", "settings"))
```

The server can override any client-set signal at any time by calling `sess.Signal(key, correctValue)`.

## Conditional bindings - derive booleans on the client

`Show` and `Class` test a signal for truthiness. When the condition is a comparison against a value the client already holds - a count, a countdown, a status string - you don't need the server to push a separate boolean. The conditional bindings evaluate the comparison in the browser and react the instant the underlying signal changes:

```go
// Show a warning only once the count passes five.
bind.Apply(warning, bind.ShowWhen("count", ">", 5))

// Hide the "all done" banner while any items remain.
bind.Apply(banner, bind.HideWhen("remaining", ">", 0))

// Turn the countdown red in the final ten seconds.
bind.Apply(timer, bind.ClassWhen("danger", "seconds", "<", 10))
```

The operator is one of `>`, `>=`, `<`, `<=`, `==`, `!=`. Numeric operands compare numerically; `==` and `!=` also compare strings and booleans, so `ShowWhen("status", "==", "error")` works too. An unknown operator panics at construction, so a typo fails fast rather than silently never matching.

The server pushes one value; the client derives however many booleans it needs:

```go
sess.Signal("count", s.Count)   // one push drives every count-based condition
```

Under the hood these three helpers compile to the same postfix programs as [computed signals](#computed-signals---derive-values-on-the-client) below and run through the one client-side VM - there is a single evaluator, not a separate comparison path. `ShowWhen("count", ">", 5)` is exactly `Computed`-style sugar for the expression `count > 5`.

## Computed signals - derive values on the client

Conditional bindings derive a boolean; computed signals derive a **value**. `Computed(name, expr)` declares a computed signal: whenever any input signal the expression reads changes, the browser re-evaluates `expr` and publishes the result under `name`, driving every binding on `name` (`Text`, `Show`, `Class`, `Attr`, ...) with no server round-trip.

```go
// A live cart total from two server-pushed signals.
bind.Apply(span.New(),
    bind.Computed("cart.total", "cart.qty * cart.price"),
    bind.Text("cart.total"),
)
```

The four motivating cases, each a one-liner:

```go
// 1. Cart total: quantity times price.
bind.Computed("cart.total", "cart.qty * cart.price")

// 2. Character counter: characters left before the limit.
bind.Computed("chars.left", "280 - len(draft)")

// 3. "N selected": a label built by concatenation.
bind.Computed("sel.label", "selected + ' selected'")

// 4. Enable submit only when the form is valid.
bind.Computed("form.ok", "len(email) > 3 and agreed")
```

### The operator set (closed list)

The expression grammar is deliberately small and **has no function-call syntax**, so nothing outside this table can appear. `len` is a fixed unary operator, not a callable function.

| Category | Operators |
| --- | --- |
| Arithmetic | `+` `-` `*` `/` `%` |
| Comparison | `>` `>=` `<` `<=` `==` `!=` |
| Boolean | `and` `or` `not` (aliases `&&` `\|\|` `!`) |
| Unary | unary minus `-`, `not`, `len` (string or array length) |
| Grouping | parentheses |
| Literals | numbers, single- or double-quoted strings, `true`, `false` |
| Operands | signal names, including dotted keys like `cart.qty` |

`+` doubles as string concatenation when either side is a string. Comparisons work between arbitrary sub-expressions. Anything else - ternaries, indexing, member access, aggregates, function calls - is unrepresentable by design, which is what keeps the client a fixed interpreter with no `eval`.

A malformed expression (unknown operator, unbalanced parentheses, an invalid identifier, or nesting deeper than 32 levels) panics at construction with a positioned message, exactly like an unknown operator in `ShowWhen`.

### Two rules

- **Declare a name once.** A computed name is owned by its expression. Do not declare the same name twice, and do not bind a plain server signal to a name a `Computed` also writes.
- **The server pushes inputs, never computed outputs.** Push `cart.qty` and `cart.price`; let the client derive `cart.total`. Pushing a computed output from the server fights the client for ownership of that name.

Computeds chain: a computed may read another computed's output, and the cascade resolves in one pass. A dependency cycle is detected on the client, reported via `Tether.onError`, and the offending branch is abandoned rather than looping.

## Client events - coordinate elements without the server

The event model everywhere else is client → server → client. For the cases where the server genuinely doesn't need to know - clearing a search box, closing a sibling dropdown, resetting a filter - client events let one element trigger a signal action on another with no round-trip.

`Emit` dispatches a named event to the elements matching a selector on click; `OnClientEvent` runs a `SetSignal` or `ToggleSignal` on the receivers when that event arrives, so they react through the ordinary signal bindings:

```go
// A search input bound to the "query" signal...
bind.Apply(input.New(),
    bind.Value("query"),
    bind.OnClientEvent("clear", bind.SetSignal("query", "")),
)

// ...cleared by a button that emits "clear" to it.
bind.Apply(clearButton, bind.Emit("clear", "#search"))
```

Only `SetSignal` and `ToggleSignal` are valid actions - anything else panics - because the receiver reacts through signals, which are the client's single source of truth. That keeps client events composable with `Show`, `Value`, `Class`, and the conditional bindings above, rather than introducing a second, parallel way to change the DOM.

## Optimistic updates - predict and correct

For predictable actions where the round-trip delay would feel sluggish, update a signal immediately before the event reaches the server. When the server responds, its signals overwrite the optimistic value - if the prediction was wrong, the DOM corrects itself.

```go
// Set a signal to a specific value immediately, then send the event
bind.Apply(button.Text("Like"),
    bind.OnClick("like"),
    bind.Optimistic("liked", "true"),
)

// Toggle a boolean signal immediately, then send the event
bind.Apply(button.Text("Like"),
    bind.OnClick("like"),
    bind.OptimisticToggle("liked"),
)
```

## Client-side timers - autonomous ticking

Timers tick entirely in the browser. The server controls them by pushing signals - no background goroutines and no per-tick WebSocket messages:

```go
// Render: attach a timer to an element
bind.Apply(span.New(), bind.Timer("elapsed"))

// Handle or OnConnect: start the timer
sess.Signal("elapsed.running", true)
```

The timer increments (or decrements) a signal locally on each tick and updates the element's text content with the formatted value. See [client-side timers](client-side.md#timers) for the full API including countdown, precision, and completion events.

## Client-side directives - ephemeral state

For toggle-only UI (drawers, menus, modals) where the server doesn't need to know, use client-side directives. These are covered in [client-side features](client-side.md).

## Signal truthiness

`Show`, `Hide`, `Class`, and `Attr` evaluate signal values
for truthiness. The signal store always holds properly typed values:

- **Server → client**: `Signal("flag", false)` serialises to JSON `false`
  (boolean). `Signal("count", 42)` serialises to JSON `42` (number).
- **Client-side signal actions**: `SetSignal` and `Optimistic` read string
  values from HTML data attributes, but the runtime parses them to proper
  types before storing: `"true"` → `true`, `"false"` → `false`, `"42"` →
  `42`. Plain text stays as strings.

Because values are always typed, truthiness follows standard rules:

| Value | Falsy? |
|-------|--------|
| `false` | Yes |
| `0` | Yes |
| `""` (empty string) | Yes |
| `nil` / `null` | Yes |

Everything else is **truthy** - `true`, non-zero numbers, non-empty strings.

**Always use Go booleans for boolean signals.** `Signal("flag", false)` is
correct. `Signal("flag", "false")` stores the string `"false"` which is a
non-empty string and therefore **truthy** - this is a bug in your code, not
a framework issue.

## Don't mix signals and state rendering on the same element

An element should be driven by **either** signals **or** state rendering - never both. If a signal updates an element's text and the next render cycle also touches that element, the render overwrites the signal value:

```go
// Wrong - the render and signal fight over the same element
bind.Apply(span.Textf("Count: %d", s.Count).Dynamic("count"), bind.Text("count"))

// Right - signal-only element, no Dynamic key needed
bind.Apply(span.New(), bind.Text("count"))

// Right - state-rendered element, no signal binding
span.Textf("Count: %d", s.Count).Dynamic("count")
```

One refinement is allowed, and it is the flash-free way to seed: a
signal-bound element may take its *initial* text from state, so the first
paint is correct without waiting for the first signal push:

```go
// Right - state provides the initial value, the signal owns it after
bind.Apply(span.Text(strconv.Itoa(s.Count)), bind.Text("count"))
```

This is safe on two conditions: the element carries no Dynamic key, and the
handler keeps the state field and the signal in step (whatever bumps the
signal bumps the state), so a later full morph repaints the same value the
signal last wrote.

Pick one update path per element - state may seed a signal-bound element's
initial value, but only the signal writes to it afterwards. Use signals for
high-frequency updates that bypass rendering. Use state rendering for
everything else.

## When to use what

| Mode | Round-trip | Use case |
|------|-----------|----------|
| **Server rendering** | Yes | Structural changes, conditional rendering, list updates |
| **Signals** | Server → client only | Counters, status indicators, progress bars |
| **Signal actions** | None | Tab selection, menu state with server override |
| **Optimistic updates** | Yes (with instant preview) | Like buttons, checkboxes, toggles |
| **Client directives** | None | Drawer open/close, modal visibility |
| **Client-side timers** | Start/stop only | Elapsed time, countdowns, stopwatches |

---

[← Back to documentation](../README.md#documentation)
