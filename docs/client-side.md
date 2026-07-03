# Client-side features

## Directives

Toggle CSS classes or attributes without a server round-trip:

```go
// Toggle a CSS class on the element itself
bind.Apply(button.Text("Menu"), bind.ToggleClass("is-open"))

// Toggle a CSS class on a different element
bind.Apply(button.Text("Menu"), bind.ToggleTarget("#nav"), bind.ToggleClass("is-open"))

// Toggle visibility via the hidden attribute
bind.Apply(button.Text("Show Help"), bind.ToggleTarget("#help"), bind.ToggleAttr("hidden"))
```

Client-managed state survives server morphs automatically. The morph engine preserves client-toggled classes and attributes so they are not lost when the server re-renders.

Prevent the morph engine from touching an element entirely (useful for video players, iframes, and third-party widgets):

```go
bind.Apply(div.New(children...), bind.Permanent())
```

Hide an element until the tether runtime initialises (prevents flash of unstyled content):

```go
bind.Apply(div.New(children...), bind.Cloak())
```

## Client-side filtering

Connect a text input to a container of items and narrow the list as the user types - no server round-trip per keystroke. The server sends the full list once; the client hides the items whose text doesn't match:

```go
bind.Apply(input.New(), bind.Filter("#item-list"))

ul.New(
    bind.Apply(li.Text("Apple"), bind.FilterItem()),
    bind.Apply(li.Text("Banana"), bind.FilterItem()),
    bind.Apply(li.Text("Cherry"), bind.FilterItem()),
).ID("item-list")
```

Matching is case-insensitive against each item's text content. Mark items with `FilterItem` to filter a specific subset; when no items are marked, every direct child of the container is treated as an item. Pair it with a [client event](signals.md#client-events---coordinate-elements-without-the-server) to add a Clear button - clearing a filter input that is bound to a signal re-runs the filter, so every item reappears.

## Signal-driven templates

Render a list on the client from a signal holding a JSON array, with no server round-trip per change. Apply `Template` to a `<template>` element whose content is the markup for one item, using `{{field}}` placeholders (or `{{.}}` for a scalar array element). The server pushes the data as a JSON array; the client stamps one clone per element into the target container:

```go
// <template> content: <li>{{name}} — {{email}}</li>
bind.Apply(itemTemplate, bind.Template("people", "#people-list"))

// Server pushes the data - the client re-renders the list locally.
sess.Signal("people", people)
```

Interpolated values are HTML-escaped. This suits client-side sorting, optimistic list additions, and filtering a list the server already sent - reactive lists on stateless pages without upgrading to a WebSocket. The `tether-template.js` extension is auto-included when any element renders `data-tether-template`. For anything richer than field interpolation, keep rendering on the server.

## Scroll management

Preserve the scroll position of a container across morphs:

```go
bind.Apply(div.New(messages...).Class("chat-feed"), bind.PreserveScroll())
```

Without this, the morph engine may reset the scroll position when the container's content changes. Use this on columns, chat feeds, and any scrollable region where the user has scrolled to a specific position.

Auto-scroll a container to the bottom after each morph:

```go
bind.Apply(pre.New(text.Text(logOutput)).Class("log-viewer"), bind.AutoScroll())
```

Use this on log viewers, streaming output, and terminal-style displays where new content appears at the bottom. After every morph that updates the container's content, the client scrolls to the latest entry automatically.

Scroll a specific element into view on click (client-side, no server round-trip):

```go
bind.Apply(button.Text("Jump to bottom"), bind.ScrollTo("#latest"))
```

## Transitions

CSS transitions coordinated with the morph lifecycle:

```go
bind.Apply(div.New(children...), bind.Transition("fade"))
```

```css
.item { opacity: 1; transition: opacity 0.3s; }
.tether-fade-enter { opacity: 0; }
.tether-fade-leave { opacity: 0; }
```

Enter: `tether-{name}-enter` is added before insertion and removed next frame. Leave: `tether-{name}-leave` is added and the node waits for `transitionend` before removal (`TransitionTimeout` fallback, default 5s).

## Timers

Client-side timers tick entirely in the browser. The server controls them by pushing signals - no background goroutines and no per-tick WebSocket messages required. The timer runtime ships as an extension script (`tether-timer.js`) that the framework includes automatically whenever a rendered page uses `bind.Timer`.

### Basic elapsed timer

```go
// Attach a timer to an element. The element's text content is
// automatically bound to the formatted timer value.
bind.Apply(span.New(), bind.Timer("elapsed"))
```

Control the timer from the server by pushing signals:

```go
sess.Signal("elapsed.running", true)   // start
sess.Signal("elapsed.running", false)  // pause
sess.Signal("elapsed", 0)             // reset
```

The timer counts up from zero at one-second precision with an auto-detected display format (`ss` under a minute, `mm:ss` under an hour, `hh:mm:ss` beyond that).

### Countdown with completion event

```go
bind.Apply(span.New(),
    bind.Timer("quiz"),
    bind.Countdown(30*time.Second),
    bind.TimerOnComplete("quiz.expired"),
)
```

When the countdown reaches zero, it stops automatically and fires `quiz.expired` back to the server as a standard event:

```go
case "quiz.expired":
    sess.Toast("Time's up!")
```

### Sub-second precision

```go
bind.Apply(span.New(),
    bind.Timer("stopwatch"),
    bind.TimerPrecision(100*time.Millisecond),
    bind.TimerFormat("mm:ss.S"),
)
```

### Configuration

All options have sensible defaults. Stack them with `bind.Apply` like any other binding:

| Option | Default | Description |
|--------|---------|-------------|
| `bind.Timer(name)` | (required) | Attaches a timer and binds the element's text |
| `bind.Countdown(d)` | Count up | Count down from `d` instead of counting up |
| `bind.TimerPrecision(d)` | 1 second | Tick interval |
| `bind.TimerFormat(pattern)` | Auto | Display format (see below) |
| `bind.TimerOnComplete(action)` | None | Event fired when a countdown reaches zero |

### Signal convention

For a timer named `foo`:

- `foo` - the current value in seconds (number)
- `foo.running` - boolean controlling start/pause

These are standard signals. You can bind other elements to them with `Show`, `Class`, etc.:

```go
// Show a warning when the countdown drops below 10 seconds
bind.Apply(span.Text("Hurry!"), bind.Show("quiz.warning"))

// In Handle, push the warning signal based on the timer value
sess.Signal("quiz.warning", remaining < 10)
```

### Display formats

The `auto` format (the default) picks the shortest readable representation based on the current value. For explicit control:

| Format | Example output |
|--------|---------------|
| `ss` | `42` |
| `mm:ss` | `01:42` |
| `hh:mm:ss` | `01:01:42` |
| `mm:ss.S` | `01:42.3` |
| `mm:ss.SS` | `01:42.30` |

## JS hooks

Integrate third-party JavaScript libraries (charts, maps, rich text editors) via lifecycle hooks:

```go
bind.Apply(div.New(), bind.Hook("chart"))
```

```js
Tether.hooks.chart = {
    mounted: function(el) { /* initialise chart library */ },
    updated: function(el) { /* refresh with new data */ },
    destroyed: function(el) { /* teardown */ }
};
```

The JS runtime calls `mounted` when the element is added to the DOM, `updated` when it is morphed in place, and `destroyed` when it is about to be removed.

## Client extension API

For behaviour that spans the whole page rather than a single hooked element - keyboard layers, analytics, custom widgets - the runtime exposes a small extension API on `window.Tether`. The built-in extensions (`tether-hotkey.js`, `tether-timer.js`, `tether-select.js`, `tether-upload.js`) are written against this same surface.

### Data access

```js
Tether.getSignal("count");          // read a signal value
Tether.setSignal("count", 42);      // write it - bindings update instantly
Tether.isTruthy(value);             // the truthiness rule Show/Class use
Tether.sendEvent("click", "save", {id: "7"}); // send an event to Handle
Tether.findPrefix(el);              // resolve the data-tether-prefix chain
```

### Lifecycle subscriptions

Each subscription returns an unsubscribe function. Callbacks are guarded - a throwing listener is reported via `Tether.onError` and skipped, never breaking the core or other listeners.

```js
// After every signal change, from any source (server push, client
// signal action, another extension).
var off = Tether.onSignalChange(function (key, value) { ... });

// After each applied server update (patches, morphs, effects).
// Re-scan the DOM here for elements your extension manages.
Tether.onUpdate(function (root) { ... });

// Around morphs: an element was added / is about to be removed.
// Fires for the top-level node idiomorph touched - scan
// el.querySelectorAll for descendants.
Tether.onElementAdded(function (el) { ... });
Tether.onElementRemoved(function (el) { ... });
```

### Preserving client-managed DOM state

Server morphs rebuild elements from server HTML, which knows nothing about state your extension wrote into the DOM. Register client-managed classes and attributes so the morph carries them over:

```js
el.classList.add("my-ext-active");
Tether.trackClientClasses(el, ["my-ext-active"]);
Tether.trackClientAttrs(el, "data-my-ext-open");
```

### Binding to elements

Track per-element listener state in a `WeakSet`, not a DOM attribute - morphs sync attributes from server HTML and would strip a marker attribute while the reused node keeps its listener:

```js
var bound = new WeakSet();
function bind(el) {
    if (bound.has(el)) return;
    bound.add(el);
    el.addEventListener("click", ...);
}
Tether.onUpdate(function (root) {
    root.querySelectorAll("[data-my-ext]").forEach(bind);
});
```

---

[← Back to documentation](../README.md#documentation)
