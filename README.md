# fluent-poly

Reactive server-driven UI for [Fluent](https://github.com/jpl-au/fluent). Write Go, get live updates.

fluent-poly connects Fluent's node trees to the browser via WebSocket (with SSE fallback). When state changes, only the parts that actually changed are sent as targeted patches. The client morphs the DOM in place, preserving input focus, scroll position, and form state.

Three update modes give you the right tool for every situation:

- **Server rendering** — the default. Handle returns new state, the framework diffs and sends patches or morphs. Works for everything.
- **Signals** — push individual values from the server and bound elements update instantly with no render cycle. Ideal for counters, progress bars, and status indicators.
- **Client directives** — toggle classes, attributes, and signals entirely in the browser. Perfect for menus, modals, and optimistic updates.

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
            bind.Click(button.Text("+1"), "increment"),
        )
    },
    Handle: func(_ *poly.Session[CounterState], state CounterState, event poly.Event) CounterState {
        if event.Action == "increment" {
            state.Count++
        }
        return state
    },
}))

// Serve the client JS runtime.
mux.Handle("/_poly/", http.StripPrefix("/_poly/", poly.ServeClient()))
```

When the handler is not at root, mount the client JS runtime separately:

```go
mux.Handle("/_poly/", http.StripPrefix("/_poly/", poly.ServeClient()))
```

No WebSocket boilerplate. No JavaScript to write. No diff algorithm to understand.

## Full-page app

When the handler owns the entire page, `ListenAndServe` handles signal
trapping, graceful shutdown, and sensible defaults:

```go
h := poly.New(poly.Config[State]{
    Upgrade:      ws.Upgrade(),
    Fallback:     sse.Upgrade(),
    Mode:         mode.Auto,
    InitialState: func(r *http.Request) State { return State{} },
    Render:       render,
    Handle:       handle,
})

h.ListenAndServe("") // checks PORT env var, then defaults to :8080
```

No mux, no signal handling, no shutdown boilerplate. The handler serves
the client JS runtime, your pages, and manages the full session lifecycle.
On SIGINT or SIGTERM, sessions are drained gracefully before the process
exits.

## Embedded assets

Serve CSS, JS, and images from an `embed.FS` with automatic content-hashed URLs. Add assets to `Config.Assets` and they're auto-served — no extra mux setup needed:

```go
//go:embed static
var staticFS embed.FS

var assets = &poly.Asset{FS: staticFS, Prefix: "/static/"}

poly.New(poly.Config[State]{
    Assets: []*poly.Asset{assets},
    Layout: func(state State, content node.Node) node.Node {
        return html.New(
            head.New(assets.Stylesheet("styles.css")),
            body.New(content),
        )
    },
    // ...
})
// GET /static/styles.css?v=a1b2c3d4e5f6 → served automatically with immutable cache headers
```

## Documentation

| Guide | Description |
|-------|-------------|
| [API reference](docs/api.md) | Config, Session, Event, Middleware, polytest, bind helpers |
| [Getting started](docs/getting-started.md) | Setup, how updates reach the browser |
| [Events](docs/events.md) | Event binding, timing, loading states, forms |
| [Signals](docs/signals.md) | Reactive signals, client directives, optimistic updates |
| [Server updates](docs/server-updates.md) | Update, Navigate, SetTitle, Flash, Announce |
| [Client-side](docs/client-side.md) | Directives, transitions, JS hooks |
| [Broadcasting](docs/broadcasting.md) | Groups, broadcast, presence |
| [Extensions](docs/extensions.md) | File uploads, service worker, push notifications |
| [Transport](docs/transport.md) | WebSocket, SSE, resilience |
| [Push notifications](docs/push-notifications.md) | Web Push with VAPID |
| [Operations](docs/operations.md) | Health check, drain, dev mode, error reporting |
| [Performance](docs/performance.md) | Benchmarks, PGO |

## Third-party libraries

| Library | Licence | Purpose |
|---------|---------|---------|
| [idiomorph](https://github.com/bigskysoftware/idiomorph) 0.3.0 | [0BSD](https://opensource.org/license/0bsd) | DOM morphing (bundled JS) |
| [coder/websocket](https://github.com/coder/websocket) | [ISC](https://github.com/coder/websocket/blob/main/LICENSE.txt) | WebSocket transport |
