# tether — Agent Guide

## What this is

Reactive server-driven UI for Go. The server owns state and renders HTML;
the browser owns ephemeral UI state (toggles, signals, transitions). A
persistent transport (WebSocket or SSE) keeps the two in sync — the server
pushes targeted DOM patches and reactive signal updates, the client forwards
user events back.

Three libraries work together:

| Library | Role |
|---------|------|
| [fluent](https://github.com/jpl-au/fluent) | HTML node trees — composable, renderable, no side effects |
| [fluent-jit](https://github.com/jpl-au/fluent-jit) | Diff engine — compares two trees, produces patches or morphs |
| **tether** | Session management, transport, wire protocol, command loop |

fluent builds the tree. fluent-jit diffs it. tether orchestrates the
lifecycle.

## Package structure

```
tether/              Root package — Config, Handler, Session, Bus, Group, Value, Observe, On
├── bind/            Event binding and signal binding via Apply + composable Options (OnClick, BindText, Confirm, etc.)
├── client/          Embedded JS runtime (tether.js, idiomorph, service worker, upload, push worker)
├── dev/             Debug logging — dev.Enable() activates, dev.Debug() is a no-op when disabled
├── docs/            Markdown guides (architecture, API, events, signals, broadcasting, etc.)
├── event/           Event type constants (click, input, submit, navigate, etc.)
├── mode/            Transport mode constants (WebSocket, ServerSentEvents, Both, HTTP)
├── push/            Web Push notification support (VAPID, Sender, Subscription)
├── router/          Multi-page routing — dispatches Render/Handle to the active page by URL path
├── sse/             SSE+POST transport implementation
├── tethertest/      Test harness — drives Handle functions and component dispatch without transport or goroutines
├── wire/            Wire protocol — Update struct, Encoder interface, JSON encoding
└── ws/              WebSocket transport implementation
```

## Key types

### Config[S] and Handler[S]

`Config[S]` wires everything together: `InitialState`, `Render`, `Handle`,
transport upgraders, middleware, lifecycle callbacks, timeouts, and limits.
`New(cfg)` returns a `*Handler[S]` which is an `http.Handler`.

Required fields: `InitialState`, `Render`, `Handle`, and at least one of
`Upgrade` or `Fallback` (depending on `Mode`).

### Session (interface)

The interface every handler receives. Provides side-effect methods (`Toast`,
`Signal`, `Navigate`, `SetTitle`, `Announce`, `Flash`, `Push`, `Go`). Works
identically in live mode, stateless page mode, and tests. `OnNavigate`,
`Handle`, and the test harness all receive `Session`. `Bus.Emit` accepts
`Session` directly — no type-assert is needed to broadcast from `Handle`.

### LiveSession[S]

One per browser tab. Implements `Session` and adds state-aware methods:
`State` and `Update`. Owns its own state, diff engine, and command-loop
goroutine. All exported methods are safe to call from any goroutine.

Type-assert to `*LiveSession[S]` only when you need methods that are not on
`Session` — `Update` and `State`. These are lifecycle concerns and belong
in `OnConnect`/`OnDisconnect`, not in `Handle`.

### Event

Client event: `Type` (click, input, submit, navigate, etc.), `Action`
(the `data-tether-*` value), `Data` (form fields), `EventID` (for
client-side de-duplication), `Target` (set by `Config.Components` to
the mount prefix). Typed extraction helpers: `Value`, `Key`, `Get`,
`Int`, `Float64`, `Bool`, `Bind`. `WithAction` returns a copy with a
different Action (used by Route, RouteTyped, and mounts for prefix
stripping).

### Params

Navigation context passed to `Config.OnNavigate` and
`router.OnNavigate`. Carries `Path` (URL path) and `Query`
(`url.Values`). Provides typed extraction helpers that mirror `Event`'s
API for consistency — `Get`, `Int`, `Float64`, `Bool`. Also provides
soft getters — `IntOr`, `Float64Or`, `BoolOr` — that return a default
when the key is missing or the value is malformed, which is the common
case for optional URL parameters. Multi-value helpers — `Strings`,
`Ints`, `Float64s` — handle repeated query keys. Defined in
`params.go`.

### HandleFunc[S]

`func(session Session, state S, event Event) S` — processes a client
event and returns the new state. Runs inside the command loop; must not
block.

### Bus[E]

Typed pub/sub for cross-handler communication. `Bus.Emit(sess, event)`
accepts `Session` directly — no type-assert needed in `Handle`.
Publishes with sender filtering: subscriptions whose session ID matches
the emitter are skipped, preventing double-apply. In live sessions,
publication is enqueued on the sender's command loop so the sender's
diff is delivered before other subscribers react.

`Bus.Publish(event)` publishes with no sender filter — use for external
sources (database listeners, message queues, cron jobs). Two raw
subscription modes: `Subscribe` (synchronous — callback runs in the
publisher's goroutine, must not block) and `SubscribeAsync` (asynchronous
— callback runs in its own goroutine per event, safe for I/O). `On` wraps
the callback in `s.Update` so it runs in the subscriber's command loop.

Register raw subscriptions via a `Setup(ctx context.Context)` function
called from `main` with the root context so they cancel on shutdown.
Avoid `init()` — subscribers registered there have no cancellation path.

Lock-free reads via `atomic.Value`, copy-on-write for writes.

### Group[S]

Session pool for broadcasting. `Add`/`Remove` in `OnConnect`/`OnDisconnect`
(or use `Config.Groups` for automatic membership). `Broadcast(fn)` calls
`Update` on every member — non-blocking. `BroadcastOthers` skips the
sender. Lock-free reads, copy-on-write writes.

### Value[V]

Thread-safe shared state with observer notifications. `Store`/`Update`
publish to all observers via an internal `Bus`. `Load` is lock-free.
`Observe(session, val, fn)` subscribes a session — the initial value and
subscription happen atomically within a single session command to prevent
stale overwrites.

### Watcher[S]

Declarative reactive subscription for Config. `WatchValue(val, mapper)`
creates a watcher that observes a Value; `WatchBus(bus, mapper)` creates
one that subscribes to a Bus. Listed in `Config.Watchers`, they are
subscribed automatically before `OnConnect` runs.

### DiffStore

`DiffStore` is an interface with three methods: `Save`, `Load`, and `Delete`.
The framework calls `Save` when a session disconnects (persisting the differ
snapshot to external storage) and `Delete` when the session reconnects or is
destroyed. `Load` is included for tooling and debugging but is not called by
the framework today — reconnecting sessions re-render from state, which
re-seeds the differ.

Nil by default (opt-in via `Config.DiffStore`). No first-party implementations
are provided — developers supply their own, backed by whatever storage suits
their deployment (SQLite, Redis, filesystem, etc.).

### SessionStore

`SessionStore` persists the developer's application state `S` plus session
metadata for crash recovery and node migration. The framework saves on
disconnect and graceful shutdown, loads on crash recovery (reconnecting client
hits a server with no in-memory session), and deletes after successful restore
or on destroy.

The codec (`SessionCodec[S]`) serialises `S` — CBOR by default, custom codec
via `Config.Codec`. The framework wraps the codec output in an envelope with
session metadata (endpoint, URL, title, user-agent) before passing to the
store.

`OnRestore` fires instead of `OnConnect` for restored sessions. Falls back to
`OnConnect` when nil. Nil by default (opt-in via `Config.SessionStore`).

### Component

`Component` is a self-contained rendering unit — `Render() node.Node` builds
the UI, `Handle(Session, Event) Component` processes events. Components are
value types; Handle returns a new value, the receiver is not mutated. They
receive `Session` (not `*LiveSession`), so they work in SSR pre-warming and
tests without special cases.

`EqualComponent` is an optional interface for fast equality checking.

`Route` and `RouteTyped` dispatch events by prefix in Handle. `RouteTyped`
preserves the concrete type for compile-time safety.

`Config.Components` with `Mount` wires components declaratively — the framework
intercepts events by prefix and dispatches them before Handle runs. Navigate
events bypass mounts.

`Event.Target` is set by the mount system to the prefix. `Event.WithAction`
creates event copies with a different action for prefix stripping.

`Mounter` is an optional interface (`Mount(Session) Component`) for one-time
setup. The framework calls it during session startup for Config.Components
mounts. Components that don't need setup omit it.

`PageConfig.Components` mirrors `Config.Components` for stateless pages —
same `RouteMount` dispatch before Handle, same `Mount` constructor.

### Router[S] (router package)

Multi-page routing. Maps URL paths to `Page[S]{Render, Handle}` pairs.
`router.Render` and `router.Handle` dispatch to the active page based on
a selector function. Lock-free dispatch via `atomic.Value`.

## Concurrency model

### The command loop

Every session has a single goroutine (`run()` in `loop.go`) that processes
three channels:

- `events` — client events from the transport reader goroutine
- `cmds` — commands from `Update`, `Broadcast`, `Observe`, bus callbacks
- `fxCh` — side effects (Toast, Signal, Navigate) arriving outside Handle

All state mutations happen inside this goroutine. No mutexes in the hot
path.

### Overflow handling

The `cmds` and `fxCh` channels are buffered (default 64, configurable via
`Limits.CmdBufferSize`). When full, a short-lived goroutine delivers the
command instead of blocking the caller. This prevents cross-session
deadlocks during broadcast storms. Each overflow emits a `BufferOverflow`
diagnostic via `Handler.Diagnostics`.

Overflow goroutines are capped by a semaphore sized to `CmdBufferSize`. When
both the buffer and the semaphore are full, the command is dropped and a
`CommandDropped` diagnostic is emitted — this signals data loss.

See [operations](docs/operations.md#diagnostics-bus) for the full list of
diagnostic kinds and subscription examples.

### State snapshots

`Session.State()` has three paths:
1. **Inside Handle** (`handling` is true) — returns an atomic snapshot
   captured before Handle started. No channel hop, no deadlock.
2. **Loop not yet started** — returns `s.state` directly.
3. **Outside Handle** — synchronous read through the command channel.

### Effect buffering

Side effects called during Handle (`Toast`, `Signal`, `Navigate`, etc.)
are buffered on `fxCh` and drained after Handle returns. They are merged
into the same update message as the diff — the client receives state
changes and effects in a single frame.

Effects called outside Handle (from `Go` goroutines, timers, bus callbacks)
are sent as standalone updates.

## Session lifecycle

Sessions move through three pools in `Handler`:

```
Pending  →  Active  ⇄  Disconnected  →  Destroyed
```

- **Pending**: created during the initial GET. State and diff engine are
  pre-warmed. Cleaned up after `Timeouts.Pending` (default 30s) if the
  browser never connects.
- **Active**: transport connected, command loop running.
- **Disconnected**: transport lost but session alive for
  `Timeouts.Reconnect` (default 30s). Commands, broadcasts, and timers
  continue. When a DiffStore is configured, differ snapshots are saved to
  external storage and cleared from memory during the reconnect window.
  When a SessionStore is configured, state `S` and metadata are saved for
  crash recovery. On same-node reconnect: transport swapped, store entries
  deleted (Render re-seeds the differ), full re-render sent, URL and title
  replayed. On crash recovery: session restored from SessionStore, OnRestore
  fires (or OnConnect as fallback), full update sent.
- **Destroyed**: context cancelled, loop exits, timers stopped. DiffStore
  and SessionStore entries deleted if present.

## Event pipeline

When a client event arrives, `exec()` in `loop.go` runs:

1. Track activity — update timestamp, reset idle timer
2. Snapshot state — store atomically for concurrent `State()` readers
3. Component dispatch — if Config.Components matches the event prefix, route to the component
4. Handle — if no component matched, call the page handler
5. Drain effects — collect buffered Toast/Signal/Navigate calls
6. Equality check — skip render if `Equal` says state unchanged
7. Render — build a new node tree
8. Diff — compare with the previous tree via fluent-jit
9. Send — serialise patches + effects, push to the client

## Transport

The `Transport` interface has three methods: `Send([]byte) error`,
`ReceiveEvent() (Event, error)`, `Close() error`.

| Transport | Server → Client | Client → Server |
|-----------|----------------|-----------------|
| WebSocket (`ws/`) | Text frames | Text frames |
| SSE+POST (`sse/`) | `text/event-stream` | Individual HTTP POST requests |

The client JS tries WebSocket first and falls back to SSE. SSE transports
send heartbeat comments at `Timeouts.Heartbeat` (default 20s) to keep
proxy connections alive.

## Wire protocol

Server-to-client updates are JSON objects containing any combination of:
`patches` (targeted content updates keyed by Dynamic key), `morphs`
(structural DOM changes via idiomorph), `url`, `title`, `toast`, `flash`,
`signals`, `announce`, `eventID`.

**Patches** are the common case — a Dynamic-keyed element's HTML changed.
**Morphs** are the fallback when the set of Dynamic keys changed between
renders (keys added, removed, or reordered).

## Stateless pages

`tether.Page(PageConfig[S])` creates an `http.Handler` for pages without
persistent connections. GET renders HTML, POST handles an event and returns
a JSON update. Same wire format as live mode. Same `Handle` signature. No
session pool, no command loop, no goroutines.

## Testing

`tethertest.New(cfg)` creates a test harness that wraps `tether.Page`
internally. Send events with `h.Send("action")`, `h.SendInput(...)`,
`h.SendSubmit(...)`, or `h.Navigate("/path")`. Inspect results with
`h.State()`, `h.HTML()`, `h.Toast()`, `h.Signals()`, etc.

No transport, no goroutines, no channels — each `Send` is a synchronous
HTTP round-trip via `httptest`.

Integration tests for the live session system use `testing/synctest` for
deterministic concurrency testing.

## Cross-session communication

| Primitive | Parameterised on | Use case |
|-----------|-----------------|----------|
| **Group[S]** | State type | Broadcast state mutations to sessions of the same handler |
| **Bus[E]** | Event type | Discrete domain events across handlers (chat messages, notifications) |
| **Value[V]** | Value type | Shared observable state all sessions should track (online count, config) |

## Diagnostics

`Handler.Diagnostics` is a `Bus[Diagnostic]` that emits framework-level
events: transport errors, encode failures, panics, buffer overflows, and
upload errors. Subscribe for metrics, alerting, or custom logging:

```go
h.Diagnostics.Subscribe(ctx, func(d tether.Diagnostic) {
    metrics.Inc("tether_" + string(d.Kind))
})
```

The framework is quiet by default — `slog` is only used for panics
(as a critical safety net). All other operational signals flow through
the diagnostic bus. `DiagnosticKind` constants: `TransportError`,
`EncodeError`, `BufferOverflow`, `CommandDropped`, `HandlerPanic`,
`UploadError`, `SessionBindingFailed`, `StoreError`. See [operations](docs/operations.md#diagnostics-bus) for
details.

Prefer `Config.Watchers` for declarative subscriptions (`WatchValue`,
`WatchBus`). Use `OnConnect` for imperative setup (incrementing counters,
publishing events, starting tickers). Do not subscribe in Handle.
Subscriptions are cleaned up automatically when the session is destroyed.

## Key files

| File | Purpose |
|------|---------|
| `config.go` | Config, Timeouts, Limits, Client, Security structs |
| `handler.go` | Handler — session pools, routing, transport upgrade |
| `diagnostic.go` | Diagnostic struct, DiagnosticKind constants, panicErr helper |
| `session.go` | Session struct, enqueue, enqueueFx, drainFx, emitDiagnostic |
| `loop.go` | Command loop (run), exec pipeline, readTransport, send |
| `methods.go` | Session methods — State, Update, Close, Toast, Navigate, Signal, Push |
| `handle.go` | HandleFunc type, middleware chain |
| `serve.go` | HTTP handler (ServeHTTP), session creation, reattach |
| `component.go` | Component interface, EqualComponent, Route, RouteTyped |
| `mount.go` | ComponentMount interface, Mount constructor, RouteMount dispatch |
| `bus.go` | Bus — typed pub/sub with atomic reads and copy-on-write |
| `group.go` | Group — session pool with Broadcast/BroadcastOthers |
| `value.go` | Value — shared observable state with Bus internally |
| `observe.go` | Observe — atomic subscribe + initial value delivery |
| `emit.go` | On — subscribe a session to a Bus with sender filtering |
| `watcher.go` | Watcher interface, WatchValue, WatchBus — declarative Config subscriptions |
| `transport.go` | Transport interface |
| `diff_store.go` | DiffStore interface for external snapshot persistence |
| `session_store.go` | SessionStore interface for session state persistence |
| `session_codec.go` | SessionCodec[S] interface + default CBOR implementation |
| `session_envelope.go` | Envelope struct — wraps encoded S with session metadata |
| `page.go` | PageConfig, stateless page handler |
| `effects.go` | Effects struct — buffers Toast, Signal, Navigate, etc. |
| `render.go` | Render helpers, tetherBody (root div with data attributes) |
| `asset.go` | Embedded asset serving with content-hashed URLs |
| `listen.go` | ListenAndServe (method + package-level), Drainable interface, signal trapping, graceful shutdown |
| `drain.go` | Graceful drain — stop accepting new sessions, wait for existing |
| `health.go` | Health check endpoint |

## Build and test

```bash
go build ./...   # compilation check (never go build .)
go test ./...    # all tests
```

Full tidy sequence before committing:

```bash
go fix ./...
goimports -w .
go fmt ./...
go vet ./...
go build ./...
go test ./...
```

## Security

Session IDs are bearer tokens — TLS is a hard requirement. User-Agent
binding is enabled by default: the framework captures the User-Agent on
session creation and verifies it on reconnect, rejecting mismatches with
a `SessionBindingFailed` diagnostic. Origin checking protects against
browser-based attacks only. Rate limiting is operator responsibility
(reverse proxy or middleware). See [security](docs/security.md) for the
full model.

## Conventions

- British spelling in comments, docs, and strings
- Short receiver names (`s` for Session, `b` for Bus, `g` for Group, `h` for Handler/Harness)
- `atomic.Value` + copy-on-write for lock-free reads on shared collections
- Effects are buffered during Handle and merged atomically with the diff
- `dev.Debug` for debug-only logging; `slog` only for panics; all other signals flow through `Handler.Diagnostics`
- `New` logs one `tether: ready` line at INFO with `transport`, optional `name`, `worker`, `middleware` count, and `dev`; no other startup noise
- `Config.Name` / `PageConfig.Name` — optional label included in startup logs to distinguish handlers that share a transport
- `context.AfterFunc` for automatic cleanup on context cancellation
