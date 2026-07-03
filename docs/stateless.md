# Stateless pages

## Overview

`tether.Stateless` creates an `http.Handler` for traditional request/response pages - no WebSocket, no SSE, no persistent connection. Each request is independent: the server reconstructs state, renders HTML, and returns the response.

Despite being stateless, pages get the same client-side features as live handlers: event binding, debounce, throttle, loading states, client-side directives, signals, transitions, and the morph engine. The only difference is the transport - events travel as individual fetch POST requests instead of a persistent channel.

Use `tether.Stateless` when you don't need server push, live updates, or broadcasting. It works well for forms, CRUD interfaces, dashboards, and any page where the server only needs to respond to user actions.

## Quick example

```go
mux.Handle("/", tether.Stateless(tether.App{}, tether.StatelessConfig[State]{
    InitialState: func(r *http.Request) State {
        return State{User: getUserFromSession(r)}
    },
    Render: render,
    Handle: handle,
}))
```

GET requests render the full page. POST requests handle a client event, render the new state, and return an update that the client applies via the morph engine - a JSON envelope by default, or plain HTML with `WireFormat: wire.HTML`.

## How it works

1. **GET** - `InitialState(r)` creates state from the request, `OnNavigate` (if set) processes URL parameters, `Render` builds the node tree, `Layout` (if set) wraps it in a document shell, and the HTML is written to the response.

2. **POST** - `InitialState(r)` reconstructs state from scratch (stateless - no memory of previous requests), `Handle` processes the event, `Render` builds the new tree, and the framework returns an update with a root morph and any side effects - a JSON envelope by default, or plain HTML with `WireFormat: wire.HTML`. The client morphs the page in place.

The POST response uses the same wire format as stateful mode, so the client JS handles both identically. The key difference: stateful mode sends targeted patches to keyed elements, while stateless mode always sends a full root morph (since there is no previous tree to diff against).

## StatelessConfig

```go
app := tether.App{
    Assets:   []*tether.Asset{assets},
    Client:   tether.Client{DefaultDebounce: 200 * time.Millisecond},
    Security: tether.Security{TrustedOrigins: []string{"https://example.com"}},
    DevMode:  true,
}

tether.Stateless(app, tether.StatelessConfig[State]{
    // Required: reconstruct state from the HTTP request.
    // Called on every request (GET and POST). Derive state from
    // the URL, cookies, headers, or a database - not from r.Body.
    InitialState: func(r *http.Request) State { ... },

    // Required: build a node tree from the current state.
    Render: func(s State) node.Node { ... },

    // Required: process a client event and return the new state.
    // The session parameter is a Session - call Toast, Navigate,
    // Flash, etc. for side effects.
    Handle: func(sess tether.Session, s State, ev tether.Event) State { ... },

    // Optional: process URL parameters on every request.
    // Called after State on both GET and POST.
    OnNavigate: func(sess tether.Session, s State, p tether.Params) State { ... },

    // Optional: wrap page content in a full HTML document.
    Layout: func(s State, content node.Node) node.Node { ... },

    // Optional: declarative component mounts (same as StatefulConfig.Components).
    Components: []tether.ComponentMount[State]{
        tether.Mount("widget", getWidget, setWidget),
    },

    // Optional: response encoding for POST events. wire.JSON (the
    // default) shares the stateful envelope; wire.HTML answers with
    // plain HTML fragments - curl-inspectable, no envelope.
    WireFormat: wire.HTML,

    // Optional: Cache-Control for GET responses. Stateless pages
    // embed no session token, so they are safe to cache when the
    // content permits it.
    CacheControl: "public, max-age=60",

    // Optional configuration.
    Limits: tether.Limits{MaxEventBytes: 128 << 10},
})
```

### Fields compared to StatefulConfig

| Feature | `tether.Stateless` | `tether.Stateful` |
|---------|-------------|------------|
| State creation | `InitialState(r)` - every request | `InitialState(r)` - once per session |
| Transport | HTTP POST/response | WebSocket or SSE |
| Handle parameter | `Session` only | `Session` (type-assert to `*StatefulSession` for Update and State) |
| Server push | No | Yes (Update, Signal, Toast from any goroutine) |
| OnConnect/OnDisconnect | No | Yes |
| Groups/Broadcast | No | Yes |
| Bus/Value/Observe | No | Yes |
| File uploads | No | Yes (UploadConfig) |
| Push notifications | No | Yes (PushConfig) |
| Service worker | No | Yes (Worker) |
| Signals | Client-side only | Server push + client-side |
| Dynamic keys | Optional (needed for `sess.Morph`) | Required for targeted patches |
| Targeted updates | Explicit via `sess.Morph("key")` | Automatic via differ |

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

This is the fundamental difference from stateful mode: stateful handlers accumulate state in memory across events, while stateless handlers reconstruct it each time.

## Targeted morphs

By default, every POST response contains a root morph - the full rendered tree. The client morphs the entire page via idiomorph, which is efficient (it only touches changed DOM nodes), but the server still serialises and transmits the full HTML.

When you know which parts of the page an event affects, call `sess.Morph` to return only those subtrees instead:

```go
func handle(sess tether.Session, s State, ev tether.Event) State {
    if ev.Action == "increment" {
        s.Count++
        sess.Morph("count")
    }
    return s
}

func render(s State) node.Node {
    return div.New(
        span.Textf("Count: %d", s.Count).Dynamic("count"),
        span.Text("This large sidebar never changes").Dynamic("sidebar"),
        // ... more content
    )
}
```

The response contains a single keyed morph for `"count"` instead of the full page. The client finds the element with `data-tether-key="count"` and morphs only that subtree.

### Multiple keys

Pass multiple keys in a single call or across multiple calls - they accumulate:

```go
sess.Morph("count", "title")
// or equivalently:
sess.Morph("count")
sess.Morph("title")
```

Both produce a response with two keyed morphs.

### When to use targeted morphs

Use `Morph` when the page has large static sections that the event does not affect. The savings come from not serialising and transmitting unchanged HTML. On small pages with little static content, root morphs are fine - idiomorph makes the DOM update efficient regardless.

**Do not use Morph when:**
- The event could affect any part of the page (use root morph)
- The page is small enough that the full HTML is negligible
- You are unsure which keys changed (root morph is always safe)

**Do use Morph when:**
- The page has large static sections (navigation, sidebars, tables)
- The event affects a known, bounded set of elements
- Bandwidth matters (mobile, high-latency connections)

### How it works

The full tree is always rendered - `Render(state)` runs in full so the tree is correct. The handler then walks the tree, finds nodes whose Dynamic key matches a requested key, renders each subtree individually, and returns them as keyed morphs. Keys not found in the tree are silently skipped (with a dev-mode warning).

### Automatic fragments (AutoFragments)

`sess.Morph` requires knowing which keys an event affected. When you would rather not track that, set `AutoFragments: true` and the framework works it out by content hash:

```go
tether.Stateless(app, tether.StatelessConfig[State]{
    AutoFragments: true,
    // ...
})
```

The mechanics: a stateless server holds no previous render to diff against, so the *client* keeps the state. The initial GET seeds the page with a content hash per Dynamic fragment; the client echoes the map with every event; the server hashes its fresh render and responds with only the fragments whose bytes changed, plus the refreshed map. An unchanged render sends no fragments at all (the event ID still echoes so loading state clears). If the fragment structure changes - Dynamic keys added or removed - the response falls back to a full root morph, and the protocol is self-healing: every response carries the complete map, so a missed update can only cost one redundant fragment, never a stale page.

An explicit `sess.Morph` call still takes precedence for that response. Clients that never echo hashes (curl, older pages) get plain full morphs. The cost is one extra render walk per request and a small hash map on each event - worth it on pages with large static regions, unnecessary on small pages.

### Targeted morphs vs stateful patches

In stateful mode, the differ automatically detects which Dynamic keys changed and sends targeted patches. There is no need to call `Morph` - the differ handles it. Calling `Morph` on a stateful session is a no-op with a dev warning.

In stateless mode, there is no previous tree to diff against, so targeting is either declared per-event (`sess.Morph`) or computed by content hash (`AutoFragments`), with the client carrying the hash state between requests.

| Concern | Stateful | Stateless |
|---------|----------|-----------|
| Targeting | Automatic (differ) | Explicit (`sess.Morph`) or automatic (`AutoFragments`) |
| Memory | Stores previous tree snapshots | No stored state (hashes live on the client) |
| Default | Targeted patches | Root morph |
| Developer action | Add `.Dynamic("key")` to elements | Add `.Dynamic("key")`, then `sess.Morph("key")` or `AutoFragments: true` |

## Multi-page routing

Use the `router` package for client-side navigation between pages:

```go
r := router.New[State](func(s State) string { return s.Page })
r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
r.Route("/settings", router.Page[State]{Render: settingsRender, Handle: settingsHandle})
r.NotFound(router.Page[State]{Render: notFoundRender})

tether.Stateless(tether.App{}, tether.StatelessConfig[State]{
    InitialState: func(r *http.Request) State { return State{} },
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate:   r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
    // ...
})
```

`bind.Link` anchors trigger client-side navigation - the JS runtime POSTs a navigate event instead of doing a full page load.

## Side effects

Side effects work the same as in stateful mode, but they travel in the POST response instead of over a persistent channel:

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

Note: `Session.ID()` returns an empty string in stateless mode - there is no persistent session. `Push` returns `ErrPushPreWarm`.

## The HTML wire format

Set `WireFormat: wire.HTML` and POST responses become plain HTML instead of a JSON envelope. The rendered content is the response body; side effects ride in a small JSON island appended to it:

```
$ curl -s -X POST localhost:8080/settings \
    -H 'Content-Type: application/json' \
    -d '{"type":"click","action":"save","data":{}}'
<div class="settings">...</div>
<template data-tether-effects>{"toast":"Settings saved"}</template>
```

With `sess.Morph("key")`, the body contains only the keyed fragments and the response carries a `Tether-Morph: keyed` header so the client applies them as targeted morphs:

```
$ curl -si ... | grep Tether-Morph
Tether-Morph: keyed
<span data-tether-key="count">Count: 5</span>
```

The client runtime detects the format from the response `Content-Type` - nothing to configure on the browser side, and the same page works with either format. Use `wire.HTML` when you want responses that are easy to inspect, test with curl, or serve through HTML-aware middleware; use the default `wire.JSON` when you prefer one envelope across stateless and stateful handlers.

`wire.HTML` is stateless-only: stateful transports multiplex many update kinds over one connection and need a structured format ([Stateful] fails at startup if asked for it).

## Caching stateless pages

Stateless GET responses embed no session token, so they are safe for browsers and CDNs to cache when the content permits it. Set `CacheControl` to opt in:

```go
CacheControl: "public, max-age=60, stale-while-revalidate=300",
```

The default sends no `Cache-Control` header in production; dev mode always sends `no-store` so edits show immediately.

## When to upgrade to stateful mode

Start with `tether.Stateless` and upgrade to `tether.Stateful` when you need:

- **Server push** - the server initiating updates without a client event (timers, database changes, external webhooks)
- **Broadcasting** - pushing updates to multiple users simultaneously
- **Background goroutines** - long-running work tied to a session's lifetime
- **File uploads** - streaming files via the upload extension
- **Push notifications** - Web Push via the service worker

The `Render` function, `HandleFunc` signature, `OnNavigate`, `Layout`, event bindings, `StatefulConfig.Components`, and the `router` package all work identically in both modes. Upgrading typically means changing `tether.Stateless(app, StatelessConfig{...})` to `tether.Stateful(app, StatefulConfig{...})` and adding transport configuration.

---

[← Back to documentation](../README.md#documentation)
