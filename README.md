# Tether

Reactive server-driven UI for [Fluent](https://github.com/jpl-au/fluent). Write Go, get live updates.

> [!WARNING]
> **API Stability**: Tether is currently in active development and the API is not yet stable. We will break APIs freely to arrive at the best possible design before the v1.0.0 release.

**Requires Go 1.25 or later.**

Tether connects Fluent's node trees to the browser via WebSocket (with SSE fallback). When state changes, only the parts that actually changed are sent as targeted patches. The client morphs the DOM in place, preserving input focus, scroll position, and form state.

Three update modes give you the right tool for every situation:

- **Server rendering** - the default. Handle returns new state, the framework diffs and sends patches or morphs. Works for everything.
- **Signals** - push individual values from the server and bound elements update instantly with no render cycle. Ideal for counters, progress bars, and status indicators.
- **Client directives** - toggle classes, attributes, and signals entirely in the browser. Perfect for menus, modals, and optimistic updates.

## Quick example

```go
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
    Handle: func(_ tether.Session, state CounterState, ev tether.Event) CounterState {
        if ev.Action == "increment" {
            state.Count++
        }
        return state
    },
}))

// The handler is not mounted at "/", so serve the client JS runtime separately.
// A handler mounted at "/" serves it automatically - no extra mux setup.
mux.Handle("/_tether/", http.StripPrefix("/_tether/", tether.ServeClient()))
```

No JavaScript to write. No diff algorithm to understand.

## Stateful vs Stateless

Tether offers two handler modes:

**Stateful** (`tether.Stateful`) - maintains a persistent connection
(WebSocket or SSE) between browser and server. State lives in memory
across interactions. The server can push updates at any time. Use this
for dashboards, chat, real-time collaboration - anything where the
server needs to push updates or maintain session state.

**Stateless** (`tether.Stateless`) - traditional HTTP request/response.
State is reconstructed from each request. No persistent connection, no
session pool. Use this for forms, navigation, and pages where each
interaction is independent. See [stateless pages](docs/stateless.md)
for details.

Both modes share the same rendering engine, event system, and bind
helpers. The difference is how state is managed - persistent or
per-request.

## Escaping and keys

Fluent escapes attribute values at set time and filters URL attributes,
so application state that flows into a render cannot break out of an
attribute. `SetAttributeRaw` stores a value verbatim for the rare case
where trusted markup needs to bypass this. The initial page is written
straight to the response with Fluent's `WriteTo`; everything after that
is a diff.

Diffing is keyed. Mark the changing parts of the tree with
`.Dynamic("key")` and the differ tracks each one individually; the
client morphs the matching `data-fluent-key` element in place. Fragments
wrapped in `jit.Memoise` render once and are reused until their inputs
change. See [engine](docs/engine.md) for how the Differ and Memoiser
compose.

## Standalone server

When your application is a single handler, `ListenAndServe` handles
signal trapping, graceful shutdown, and sensible defaults:

```go
h := tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
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

Serve CSS, JS, and images from an `embed.FS` with automatic content-hashed URLs. Add assets to `App.Assets` and they're auto-served - no extra mux setup needed:

```go
//go:embed static
var staticFS embed.FS

var assets = &tether.Asset{FS: staticFS, Prefix: "/static/"}

app := tether.App{
    Assets: []*tether.Asset{assets},
}

tether.Stateful(app, tether.StatefulConfig[State]{
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

## Persistence

Two independent, opt-in stores handle different concerns:

**Session store** - persists application state `S` for crash recovery and node
migration. Set `StatefulConfig.SessionStore` to enable. On disconnect and graceful
shutdown, the framework serialises `S` (CBOR by default) and saves it. When a
reconnecting client hits a server with no in-memory session, the framework
restores from the store. See [session-store](docs/session-store.md) for details.

**Diff store** - offloads differ snapshots to external storage during the
reconnect window, freeing Go memory. Set `StatefulConfig.DiffStore` to enable. This is
a memory optimisation, not a recovery mechanism. See [store](docs/store.md) for
details.

## Cluster

Bus and Value are in-process by default. For multi-node deployments,
set a Cluster on App and add topic names to the primitives you want
distributed:

```go
app := tether.App{
    Cluster: tetheredis.New(rdb),
}
var messages = tether.NewBus[Message](tether.BusConfig{Topic: "messages"})
var online   = tether.NewValue(0, "online-count")
```

Events and values are serialised with CBOR and routed through the
broker. Groups remain local - cross-node broadcasting flows through
Bus events. See [cluster](docs/cluster.md) for details.

## Diagnostics

`Handler.Diagnostics` is a typed event bus for framework-level signals -
transport errors, panics, buffer overflows, and more. Subscribe for metrics,
alerting, or custom logging. See [operations](docs/operations.md#diagnostics-bus)
for details and examples.

## <a id="documentation"></a>Documentation

| Guide | Description |
|-------|-------------|
| [Architecture](docs/architecture.md) | Core concepts, session lifecycle, command loop, transport |
| [Reactivity](docs/reactivity.md) | Observer pattern, event-driven design, how Bus/Value/Group compose |
| [API reference](docs/api.md) | App, StatefulConfig, Session, Event, Component, Middleware, tethertest, bind helpers |
| [Getting started](docs/getting-started.md) | Setup, how updates reach the browser |
| [Stateless pages](docs/stateless.md) | `tether.Stateless` for request/response pages without persistent connections |
| [Events](docs/events.md) | Event binding, timing, loading states, forms |
| [Signals](docs/signals.md) | Reactive signals, client directives, optimistic updates |
| [Server updates](docs/server-updates.md) | Side effects, Dynamic keys, diff observability |
| [Background goroutines](docs/background.md) | `Session.Go`, transport vs session context, session methods |
| [Components](docs/components.md) | `tether.Component`, declarative mounting, `RouteTyped`, `Mounter` |
| [URL routing](docs/routing.md) | `OnNavigate`, `bind.Link`, multi-page apps with the `router` package |
| [Client-side](docs/client-side.md) | Directives, transitions, JS hooks |
| [Broadcasting](docs/broadcasting.md) | Groups, broadcast, presence |
| [Cluster](docs/cluster.md) | Cross-node Bus and Value via Redis or other brokers |
| [Extensions](docs/extensions.md) | File uploads, service worker, push notifications |
| [SessionStore](docs/session-store.md) | Session state persistence for crash recovery and node migration |
| [Frozen mode](docs/frozen-mode.md) | Zero-memory disconnected sessions via `Freeze` |
| [DiffStore](docs/store.md) | External snapshot persistence for disconnected sessions |
| [Transport](docs/transport.md) | WebSocket, SSE, resilience |
| [Reconnection](docs/reconnection.md) | Client reconnection backoff, jitter, tuning |
| [WASM runtime](docs/wasm.md) | Experimental Go WASM client as an alternative to `tether.js` |
| [Push notifications](docs/push-notifications.md) | Web Push with VAPID |
| [Operations](docs/operations.md) | Health check, drain, dev mode, diagnostics, error reporting |
| [Error catalogue](docs/errors.md) | Client-side error/warning slugs surfaced via `Tether.onError` |
| [Scaling](docs/scaling.md) | Per-session overhead, horizontal scaling, capacity planning |
| [Security](docs/security.md) | TLS, session identity, origin checking, CSRF, rate limiting |
| [Best practices](docs/best-practices.md) | Common patterns, performance tips, pitfalls to avoid |
| [Engine](docs/engine.md) | Differ, Memoiser, Patch, coalescing, and how they compose |
| [Performance](docs/performance.md) | Benchmarks, windowing, PGO |
| [Windowing](docs/windowing.md) | Virtual scrolling for large lists |

## Third-party libraries

| Library | Licence | Purpose |
|---------|---------|---------|
| [idiomorph](https://github.com/bigskysoftware/idiomorph) 0.7.4 | [0BSD](https://opensource.org/license/0bsd) | DOM morphing (bundled JS) |
| [lxzan/gws](https://github.com/lxzan/gws) | [Apache-2.0](https://github.com/lxzan/gws/blob/main/LICENSE) | WebSocket transport |
| [fxamacker/cbor](https://github.com/fxamacker/cbor) | [MIT](https://github.com/fxamacker/cbor/blob/master/LICENSE) | CBOR encoding for session state persistence |
| [fsnotify](https://github.com/fsnotify/fsnotify) | [BSD-3-Clause](https://github.com/fsnotify/fsnotify/blob/main/LICENSE) | Filesystem watching for `Asset.WatchDir` |
| [andybalholm/brotli](https://github.com/andybalholm/brotli) | [MIT](https://github.com/andybalholm/brotli/blob/master/LICENSE) | Brotli compression for SSE streams and client assets |
| [klauspost/compress](https://github.com/klauspost/compress) | [BSD-3-Clause](https://github.com/klauspost/compress/blob/master/LICENSE) | gzip/zstd/deflate compression for SSE streams and client assets |
| [tdewolff/minify](https://github.com/tdewolff/minify) | [MIT](https://github.com/tdewolff/minify/blob/master/LICENSE) | Build-time minification of the bundled client JS (`go generate`) |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | [BSD-3-Clause](https://cs.opensource.google/go/x/crypto/+/master:LICENSE) | VAPID push notification encryption |
