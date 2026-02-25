# fluent-poly

Reactive server-driven UI for [Fluent](https://github.com/jpl-au/fluent). Write Go, get live updates.

fluent-poly connects Fluent's node trees to the browser via WebSocket (with SSE fallback). When state changes, only the parts that actually changed are sent as targeted patches. The client morphs the DOM in place, preserving input focus, scroll position, and form state.

## Quick example

```go
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

// Serve the client JS runtime.
mux.Handle("/_poly/", http.StripPrefix("/_poly/", poly.ServeClient()))
```

No WebSocket boilerplate. No JavaScript to write. No diff algorithm to understand.

## Documentation

| Guide | Description |
|-------|-------------|
| [Getting started](docs/getting-started.md) | Setup, how updates reach the browser |
| [Events](docs/events.md) | Event binding, timing, loading states, forms |
| [Server updates](docs/server-updates.md) | Update, Navigate, SetTitle, Flash, Announce |
| [Client-side](docs/client-side.md) | Directives, transitions, JS hooks |
| [Broadcasting](docs/broadcasting.md) | Groups, broadcast, presence |
| [Transport](docs/transport.md) | WebSocket, SSE, service worker, resilience |
| [Push notifications](docs/push-notifications.md) | Web Push with VAPID |
| [Operations](docs/operations.md) | Health check, drain, dev mode, error reporting |
| [Performance](docs/performance.md) | Benchmarks, PGO |

## Third-party libraries

| Library | Licence | Purpose |
|---------|---------|---------|
| [idiomorph](https://github.com/bigskysoftware/idiomorph) 0.3.0 | [0BSD](https://opensource.org/license/0bsd) | DOM morphing (bundled JS) |
| [coder/websocket](https://github.com/coder/websocket) | [ISC](https://github.com/coder/websocket/blob/main/LICENSE.txt) | WebSocket transport |
