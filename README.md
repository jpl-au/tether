# fluent-poly

Reactive server-driven UI for [Fluent](https://github.com/jpl-au/fluent). Write Go, get live updates.

fluent-poly connects Fluent's node trees to the browser via WebSocket. When state changes, only the parts that actually changed are sent as targeted patches. The client morphs the DOM in place, preserving input focus, scroll position, and form state.

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
    Upgrade: ws.Upgrade,
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

### Performance note

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
