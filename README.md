# fluent-poly

Reactive server-driven UI for [Fluent](https://github.com/jpl-au/fluent). Write Go, get live updates.

fluent-poly connects Fluent's node trees to the browser via WebSocket (with SSE fallback). When state changes, only the parts that actually changed are sent as targeted patches. The client morphs the DOM in place, preserving input focus, scroll position, and form state.

## How it works

1. Define your state type
2. Write a render function that builds a Fluent node tree, marking dynamic elements with `.Dynamic("key")`
3. Write an event handler that takes state + event and returns new state
4. Mount it as an `http.Handler`

```go
import (
    poly "github.com/jpl-au/fluent-poly"
    "github.com/jpl-au/fluent-poly/ws"
)

mux.Handle("/counter", poly.New(poly.Config[CounterState]{
    Upgrade: ws.Upgrade(),
    InitialState: func(r *http.Request) CounterState {
        return CounterState{Count: 0}
    },
    Render: func(state CounterState) node.Node {
        return div.New(
            span.Textf("Count: %d", state.Count).Dynamic("count"),
            poly.Click(button.Text("+1"), "increment"),
        )
    },
    Handle: func(state CounterState, event poly.Event) CounterState {
        if event.Action == "increment" {
            state.Count++
        }
        return state
    },
}))

// Serve the client JS runtime
mux.Handle("/_poly/", http.StripPrefix("/_poly/", poly.ServeClient()))
```

No WebSocket boilerplate. No JavaScript to write. No diff algorithm to understand.

## How updates reach the browser

fluent-poly uses a unified update protocol. Every message sent to the client is a single `"update"` type containing either **patches** (targeted content updates) or **morphs** (structural DOM changes):

```json
{"type":"update","patches":[{"key":"count","html":"<span>43</span>"}]}
{"type":"update","morphs":[{"key":"","html":"<div>...</div>"}]}
```

When only content changes (the common case), patches target specific keyed elements. When the structure changes — keys added, removed, or reordered — the server sends a root morph and the client uses [idiomorph](https://github.com/bigskysoftware/idiomorph) to update the entire root while preserving focus, scroll position, and form state.

### Structural change diagnostics

When a structural change triggers a root morph, the server logs a warning with details:

```
WARN structural change, sending root morph session=abc change="key 'help' added" bytes=15234
     tip="wrap conditional elements in a keyed container to scope this morph"
```

This tells you exactly what changed and how to avoid the cost. Wrapping conditional elements in a stable keyed container keeps morphs scoped instead of full-page.

For production telemetry, use the `OnStructuralChange` callback:

```go
poly.New(poly.Config[State]{
    OnStructuralChange: func(s *poly.Session[State], c poly.StructuralChange) {
        metrics.Counter("structural_changes").Inc()
    },
    // ...
})
```

## Event binding

Convenience helpers wrap `SetData` so you don't need to remember the `poly-*` convention strings:

```go
poly.Click(button.Text("Save"), "save")       // data-poly-click="save"
poly.Submit(form.New(children...), "create")   // data-poly-submit="create"
poly.Input(input.Text("q", ""), "search")      // data-poly-input="search"
poly.Change(dropdown, "filter")                // data-poly-change="filter"
poly.KeyDown(input.Text("cmd", ""), "exec")    // data-poly-keydown="exec"
poly.Focus(el, "focus-name")                   // data-poly-focus="focus-name"
poly.Blur(el, "blur-name")                     // data-poly-blur="blur-name"
```

These return the same element type, so chaining continues:

```go
poly.Click(button.Text("+"), "increment").Style("cursor: pointer").Class("btn")
```

Keydown events include modifier keys (`ctrl`, `shift`, `alt`, `meta`) in `Event.Data` when held.

### Timing control

Input events are debounced at `DefaultDebounce` (default 300ms). Override with `poly.Debounce`:

```go
poly.Debounce(poly.Input(input.Text("q", ""), "search"), 150)
```

Throttle any event type with `poly.Throttle`:

```go
poly.Throttle(poly.Click(button.Text("Fire"), "fire"), 1000)
```

### Loading states

Disable an element while its event is in flight to prevent double-clicks and give visual feedback:

```go
poly.Disable(poly.Click(button.Text("Save"), "save"), "Saving...")
```

The element is re-enabled when the next server update arrives. If the text argument is non-empty, the element's text content is temporarily replaced.

### Confirmation dialogs

Show `window.confirm` before sending an event:

```go
poly.Confirm(poly.Click(button.Text("Delete"), "delete"), "Are you sure?")
```

### Focus management

Direct focus to a specific element after a server update:

```go
poly.AutoFocus(input.Text("name", ""))
```

The JS runtime calls `focus()` on the first `[data-poly-focus]` element after applying patches and morphs.

## Server-initiated updates

Push state changes from outside the event loop (timers, database changes, broadcasts):

```go
session.Update(func(s State) State {
    s.Message = "New data available"
    return s
})
```

Update the page title:

```go
session.SetTitle("New Page — My App")
```

## URL routing

Bidirectional sync between Go state and the browser URL:

```go
poly.New(poly.Config[State]{
    HandleParams: func(state State, params poly.Params) State {
        state.Page = params.Path
        return state
    },
    // ...
})

// Mark an anchor for client-side navigation
poly.Link(a.Link("/profile", "Profile"))
```

Server-initiated URL changes:

```go
session.Navigate("/success")          // pushState (adds history entry)
session.ReplaceURL("/current?saved=1") // replaceState (no history entry)
```

## Client-side directives

Toggle CSS classes or attributes without a server round-trip:

```go
// Toggle a CSS class on the element itself
poly.ToggleClass(button.Text("Menu"), "is-open")

// Toggle a CSS class on a different element
poly.ToggleClass(poly.ToggleTarget(button.Text("Menu"), "#nav"), "is-open")

// Toggle visibility via the hidden attribute
poly.ToggleAttr(poly.ToggleTarget(button.Text("Show Help"), "#help"), "hidden")
```

Client-managed state survives server morphs automatically.

## Form validation

Validation is handled server-side in the `Handle` function. The key patterns:

- Wrap form + error in a `Dynamic` key so the server controls field values
- Use `poly.Preserve` to prevent JS form reset after submit
- Use `poly.Input` with a validation action for live feedback
- Keep error spans always in the tree (empty when no error) to avoid structural changes

```go
div.New(
    poly.Preserve(form.New(
        poly.Input(input.Text("text", s.TodoText), "validate-todo"),
        button.Submit("Add"),
    ).SetData("poly-submit", "add")),
    span.Text(s.TodoError).Style("color: #c33"),
).Dynamic("todo-form")
```

## Transitions

CSS transitions coordinated with the morph lifecycle:

```go
poly.Transition(div.New(children...), "fade")
```

```css
.item { opacity: 1; transition: opacity 0.3s; }
.poly-fade-enter { opacity: 0; }
.poly-fade-leave { opacity: 0; }
```

Enter: `poly-{name}-enter` is added before insertion and removed next frame. Leave: `poly-{name}-leave` is added and the node waits for `transitionend` before removal (`TransitionTimeout` fallback, default 5s).

## JS hooks

Integrate third-party JavaScript libraries (charts, maps, rich text editors) via lifecycle hooks:

```go
poly.Hook(div.New(), "chart")
```

```js
Poly.hooks.chart = {
    mounted: function(el) { /* initialise chart library */ },
    updated: function(el) { /* refresh with new data */ },
    destroyed: function(el) { /* teardown */ }
};
```

The JS runtime calls `mounted` when the element is added to the DOM, `updated` when it is morphed in place, and `destroyed` when it is about to be removed.

## Broadcasting

Push updates to multiple sessions at once:

```go
group := poly.NewGroup[State]()

poly.New(poly.Config[State]{
    OnConnect:    func(s *poly.Session[State]) { group.Add(s) },
    OnDisconnect: func(s *poly.Session[State]) { group.Remove(s) },
    // ...
})

// Send a message to every connected client
group.Broadcast(func(s State) State {
    s.Notification = "System update complete"
    return s
})
```

Sessions are updated concurrently so a slow render in one session does not block delivery to the rest. `Broadcast` returns after all updates have completed.

## Transport mode

Control which transports the handler accepts with the `Mode` field:

```go
// WebSocket only (default — Mode can be omitted)
poly.New(poly.Config[State]{
    Upgrade: ws.Upgrade(),
    // ...
})

// SSE only
poly.New(poly.Config[State]{
    Mode:     poly.SSEOnly,
    Fallback: sse.Upgrade(),
    // ...
})

// SSE with larger event buffer for high-frequency streams
poly.New(poly.Config[State]{
    Mode:     poly.SSEOnly,
    Fallback: sse.Upgrade(64), // default is 16
    // ...
})

// WebSocket with SSE fallback
poly.New(poly.Config[State]{
    Mode:     poly.WebSocketWithFallback,
    Upgrade:  ws.Upgrade(),
    Fallback: sse.Upgrade(),
    // ...
})
```

Same wire format, same API regardless of transport.

SSE connections send keep-alive comments at `HeartbeatInterval` (default 20s) to prevent proxies from closing idle connections.

## Performance note

The generic helpers add measurable overhead compared to calling `SetData` directly (~47% slower with 2 extra allocations per element). For performance-sensitive render paths, use `SetData`:

```go
button.Text("+").SetData("poly-click", "increment")
```

In practice the absolute difference is ~250ns per element — negligible next to a WebSocket round-trip — but worth knowing if you're rendering thousands of event-bound elements per frame.

## Profile-Guided Optimization (PGO)

Applications using fluent-poly benefit from [Profile-Guided Optimization](https://go.dev/doc/pgo) (Go 1.21+). PGO uses a CPU profile from your running application to make more aggressive inlining decisions at compile time. Expect **10-20% speed improvements** across the entire rendering and event-handling pipeline with no code changes.

1. Collect a CPU profile under realistic load:
   ```bash
   curl -o default.pgo http://localhost:8080/debug/pprof/profile?seconds=30
   ```
2. Place `default.pgo` in your main package directory
3. `go build` — PGO is applied automatically

Both generic helpers and direct `SetData` paths benefit equally from PGO (~5-10% each). PGO cannot eliminate the allocation difference between them — that's structural to Go's shape-based generic dispatch. Allocations are unaffected; PGO improves speed only.

## Third-party libraries

This package bundles the following third-party JavaScript library:

### idiomorph

- **Source:** https://github.com/bigskysoftware/idiomorph
- **Version:** 0.3.0
- **Licence:** BSD Zero Clause (0BSD)
- **Purpose:** DOM morphing — updates the existing DOM to match new HTML while preserving input focus, scroll position, CSS transitions, and form state.

The full licence text:

> Permission to use, copy, modify, and/or distribute this software for
> any purpose with or without fee is hereby granted.
>
> THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL
> WARRANTIES WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED
> WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE
> AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL
> DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR
> PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
> TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
> PERFORMANCE OF THIS SOFTWARE.
