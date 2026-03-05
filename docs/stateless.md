# Stateless pages

## Overview

`tether.Page` creates an `http.Handler` for traditional request/response pages — no WebSocket, no SSE, no persistent connection. Each request is independent: the server reconstructs state, renders HTML, and returns the response.

Despite being stateless, pages get the same client-side features as live handlers: event binding, debounce, throttle, loading states, client-side directives, signals, transitions, and the morph engine. The only difference is the transport — events travel as individual fetch POST requests instead of a persistent channel.

Use `tether.Page` when you don't need server push, live updates, or broadcasting. It works well for forms, CRUD interfaces, dashboards, and any page where the server only needs to respond to user actions.

## Quick example

```go
mux.Handle("/", tether.Page(tether.PageConfig[State]{
    State: func(r *http.Request) State {
        return State{User: getUserFromSession(r)}
    },
    Render: render,
    Handle: handle,
}))
```

GET requests render the full page. POST requests handle a client event, render the new state, and return a JSON update that the client applies via the morph engine.

## How it works

1. **GET** — `State(r)` creates state from the request, `OnNavigate` (if set) processes URL parameters, `Render` builds the node tree, `Layout` (if set) wraps it in a document shell, and the HTML is written to the response.

2. **POST** — `State(r)` reconstructs state from scratch (stateless — no memory of previous requests), `Handle` processes the event, `Render` builds the new tree, and the framework returns a JSON update with a root morph and any side effects. The client morphs the page in place.

The POST response uses the same wire format as live mode, so the client JS handles both identically. The key difference: live mode sends targeted patches to keyed elements, while stateless mode always sends a full root morph (since there is no previous tree to diff against).

## PageConfig

```go
tether.Page(tether.PageConfig[State]{
    // Required: reconstruct state from the HTTP request.
    // Called on every request (GET and POST). Derive state from
    // the URL, cookies, headers, or a database — not from r.Body.
    State: func(r *http.Request) State { ... },

    // Required: build a node tree from the current state.
    Render: func(s State) node.Node { ... },

    // Required: process a client event and return the new state.
    // The session parameter is a Session — call Toast, Navigate,
    // Flash, etc. for side effects.
    Handle: func(sess tether.Session, s State, ev tether.Event) State { ... },

    // Optional: process URL parameters on every request.
    // Called after State on both GET and POST.
    OnNavigate: func(sess tether.Session, s State, p tether.Params) State { ... },

    // Optional: wrap page content in a full HTML document.
    Layout: func(s State, content node.Node) node.Node { ... },

    // Optional: declarative component mounts (same as Config.Components).
    Components: []tether.ComponentMount[State]{
        tether.Mount("widget", getWidget, setWidget),
    },

    // Optional configuration.
    Assets:   []*tether.Asset{assets},
    Limits:   tether.Limits{MaxEventBytes: 128 << 10},
    Client:   tether.Client{DefaultDebounce: 200 * time.Millisecond},
    Security: tether.Security{AllowedOrigins: []string{"https://example.com"}},
    DevMode:  true,
})
```

### Fields compared to Config

| Feature | `tether.Page` | `tether.New` |
|---------|-------------|------------|
| State creation | `State(r)` — every request | `InitialState(r)` — once per session |
| Transport | HTTP POST/response | WebSocket or SSE |
| Handle parameter | `Session` only | `Session` (type-assert to `*LiveSession` for Update and State) |
| Server push | No | Yes (Update, Signal, Toast from any goroutine) |
| OnConnect/OnDisconnect | No | Yes |
| Groups/Broadcast | No | Yes |
| Bus/Value/Observe | No | Yes |
| File uploads | No | Yes (UploadConfig) |
| Push notifications | No | Yes (PushConfig) |
| Service worker | No | Yes (Worker) |
| Signals | Client-side only | Server push + client-side |
| Dynamic keys | Not needed (always root morph) | Required for targeted patches |

## Carrying state across requests

Since state is reconstructed from scratch on every POST, accumulated values (counters, lists, form input) need to travel with the event. Use `bind.EventData` to attach the current value to the element so it arrives in `Event.Data`:

```go
func render(s State) node.Node {
    return div.New(
        // The current count rides along with every click event.
        bind.Apply(button.Text("+1"),
            bind.OnClick("increment"),
            bind.EventData("count", strconv.Itoa(s.Count)),
        ),
        span.Textf("Count: %d", s.Count),
    ).Dynamic("counter")
}

func handle(_ tether.Session, s State, ev tether.Event) State {
    if ev.Action == "increment" {
        count, _ := ev.Int("count")
        s.Count = count + 1
    }
    return s
}
```

This is the fundamental difference from live mode: live handlers accumulate state in memory across events, while stateless handlers reconstruct it each time.

## Multi-page routing

Use the `router` package for client-side navigation between pages:

```go
r := router.New[State](func(s State) string { return s.Page })
r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
r.Route("/settings", router.Page[State]{Render: settingsRender, Handle: settingsHandle})
r.NotFound(router.Page[State]{Render: notFoundRender})

tether.Page(tether.PageConfig[State]{
    State:      func(r *http.Request) State { return State{} },
    Render:     r.Render,
    Handle:     r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
    // ...
})
```

`bind.Link` anchors trigger client-side navigation — the JS runtime POSTs a navigate event instead of doing a full page load.

## Side effects

Side effects work the same as in live mode, but they travel in the POST response instead of over a persistent channel:

```go
func handle(sess tether.Session, s State, ev tether.Event) State {
    if ev.Action == "save" {
        // ... save to database
        sess.Toast("Settings saved")
        sess.Navigate("/settings?saved=1")
    }
    return s
}
```

Available methods on `Session`: `Toast`, `Navigate`, `ReplaceURL`, `SetTitle`, `Announce`, `Flash`, `Signal`, `Signals`, `Push`.

Note: `Session.ID()` returns an empty string in stateless mode — there is no persistent session. `Push` returns `ErrPushPreWarm`.

## When to upgrade to live mode

Start with `tether.Page` and upgrade to `tether.New` when you need:

- **Server push** — the server initiating updates without a client event (timers, database changes, external webhooks)
- **Broadcasting** — pushing updates to multiple users simultaneously
- **Background goroutines** — long-running work tied to a session's lifetime
- **File uploads** — streaming files via the upload extension
- **Push notifications** — Web Push via the service worker

The `Render` function, `HandleFunc` signature, `OnNavigate`, `Layout`, event bindings, `Config.Components`, and the `router` package all work identically in both modes. Upgrading typically means changing `tether.Page(PageConfig{...})` to `tether.New(Config{...})` and adding transport configuration.
