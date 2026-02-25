# Getting started

## How it works

1. Define your state type
2. Write a render function that builds a Fluent node tree, marking dynamic elements with `.Dynamic("key")`
3. Write an event handler that takes state + event and returns a `HandleResult` (state + optional side effects)
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
    Handle: func(state CounterState, event poly.Event) poly.HandleResult[CounterState] {
        if event.Action == "increment" {
            state.Count++
        }
        return poly.Result(state)
    },
}))

// Serve the client JS runtime.
// Optional: pass asset URLs to precache in the service worker.
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
