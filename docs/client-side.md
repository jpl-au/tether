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

Client-side timers tick entirely in the browser. The server controls them by pushing signals - no background goroutines and no per-tick WebSocket messages required.

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

These are standard signals. You can bind other elements to them with `BindShow`, `BindClass`, etc.:

```go
// Show a warning when the countdown drops below 10 seconds
bind.Apply(span.Text("Hurry!"), bind.BindShow("quiz.warning"))

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

---

[← Back to documentation](../README.md#documentation)
