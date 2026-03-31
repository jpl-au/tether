# Getting started

## How it works

1. Define your state type
2. Write a render function that builds a Fluent node tree, marking dynamic elements with `.Dynamic("key")`
3. Write an event handler that receives the session, current state, and event - return the new state
4. Mount it as an `http.Handler`

```go
import (
    "net/http"

    "github.com/jpl-au/tether"
    "github.com/jpl-au/tether/bind"
    "github.com/jpl-au/fluent/html5/button"
    "github.com/jpl-au/fluent/html5/div"
    "github.com/jpl-au/fluent/html5/span"
    "github.com/jpl-au/fluent/node"
)

mux.Handle("/counter", tether.Stateful(tether.App{}, tether.StatefulConfig[CounterState]{
    InitialState: func(r *http.Request) CounterState {
        return CounterState{Count: 0}
    },
    Render: func(state CounterState) node.Node {
        return div.New(
            span.Textf("Count: %d", state.Count).Dynamic("count"),
            bind.Apply(button.Text("+1"), bind.OnClick("increment")),
        )
    },
    Handle: func(_ tether.Session, s CounterState, ev tether.Event) CounterState {
        if ev.Action == "increment" {
            s.Count++
        }
        return s
    },
}))

// Serve the client JS runtime (only needed when the handler is not at "/").
mux.Handle("/_tether/", http.StripPrefix("/_tether/", tether.ServeClient()))
```

No WebSocket boilerplate. No JavaScript to write. No diff algorithm to understand.

Side effects (toasts, navigation, screen reader announcements) are called directly on the session:

```go
sess.Toast("Item saved")
sess.Navigate("/success")
sess.Announce("Item added to cart")
```

## How updates reach the browser

Tether uses a unified update protocol. Every message sent to the client is a single `"update"` type containing either **patches** (targeted content updates) or **morphs** (structural DOM changes). The default wire format is JSON (`wire.JSON`):

```json
{"type":"update","patches":[{"key":"count","html":"<span>43</span>"}]}
{"type":"update","morphs":[{"key":"","html":"<div>...</div>"}]}
```

When only content changes (the common case), patches target specific keyed elements. When the structure changes - keys added, removed, or reordered - the server sends a root morph and the client uses [idiomorph](https://github.com/bigskysoftware/idiomorph) to update the entire root while preserving focus, scroll position, and form state.

The wire format is configurable via `StatefulConfig.WireFormat`. See the [transport docs](transport.md#wire-format) for details.

## Running the server

For a full-page application, `ListenAndServe` handles startup, signal
trapping, and graceful shutdown:

```go
h := tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    InitialState: func(r *http.Request) State { return State{} },
    Render:       render,
    Handle:       handle,
})

if err := h.ListenAndServe(""); err != nil {
    log.Fatal(err)
}
```

`ListenAndServe` checks the `PORT` environment variable (standard for
cloud platforms such as Cloud Run, Fly.io, and Railway), then defaults
to `:8080`. It handles SIGINT and SIGTERM, drains sessions gracefully,
and returns nil on clean shutdown. A second signal forces an immediate
exit.

The grace period defaults to 10 seconds and is configurable via
`Timeouts.ShutdownGrace`:

```go
Timeouts: tether.Timeouts{
    ShutdownGrace: 15 * time.Second,
},
```

To add routes or HTTP-level middleware alongside tether, pass a custom
mux as the second argument:

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /health", healthCheck)
mux.Handle("/{path...}", h)

h.ListenAndServe("", mux)
```

Signal handling, drain, and shutdown still work exactly the same. When
multiple tether handlers share a single mux, use the package-level
`tether.ListenAndServe` which drains and shuts down all of them:

```go
mux := http.NewServeMux()
mux.Handle("/ws/", wsHandler)
mux.Handle("/sse/", sseHandler)
mux.Handle("/", httpHandler)

tether.ListenAndServe("", mux, wsHandler, sseHandler)
```

For
sub-path mounting, use `tether.ServeClient()` to serve the client JS
runtime separately:

```go
mux.Handle("/app", h)
mux.Handle("/_tether/", http.StripPrefix("/_tether/", tether.ServeClient()))
```

---

[← Back to documentation](../README.md#documentation)
