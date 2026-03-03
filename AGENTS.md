# fluent-tether — Agent Guide

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
| **fluent-tether** | Session management, transport, wire protocol, command loop |

fluent builds the tree. fluent-jit diffs it. fluent-tether orchestrates the
lifecycle.

## Package structure

```
tether/              Root package — Config, Handler, Session, Bus, Group, Value, Observe, On
├── bind/            Event binding and signal binding helpers (Click, Submit, Input, BindText, BindShow, etc.)
├── client/          Embedded JS runtime (fluent-tether.js, idiomorph, service worker, upload, push worker)
├── dev/             Debug logging — dev.Enable() activates, dev.Debug() is a no-op when disabled
├── docs/            Markdown guides (architecture, API, events, signals, broadcasting, etc.)
├── event/           Event type constants (click, input, submit, navigate, etc.)
├── mode/            Transport mode constants (WebSocket, ServerSentEvents, Both, HTTP)
├── push/            Web Push notification support (VAPID, Sender, Subscription)
├── router/          Multi-page routing — dispatches Render/Handle to the active page by URL path
├── sse/             SSE+POST transport implementation
├── tethertest/      Test harness — drives Handle functions without transport or goroutines
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

### Session[S]

One per browser tab. Owns its own state, diff engine, and command-loop
goroutine. All exported methods (`State`, `Update`, `Toast`, `Signal`,
`Navigate`, `Go`, `Close`, etc.) are safe to call from any goroutine.

### PreSession

The subset of Session methods available before a real session exists (during
pre-warming in the initial GET) and in reusable components. `OnNavigate`,
`Handle`, and the test harness all receive `PreSession`. `Bus.Emit` accepts
`PreSession` directly — no type-assert is needed to broadcast from `Handle`.

Type-assert to `*Session[S]` only when you need methods that are not on
`PreSession` — `Update`, `Group`, `State`, or session pool operations.
These are lifecycle concerns and belong in `OnConnect`/`OnDisconnect`, not
in `Handle`.

### Event

Client event: `Type` (click, input, submit, navigate, etc.), `Action`
(the `data-tether-*` value), `Data` (form fields), `EventID` (for
client-side de-duplication).

### HandleFunc[S]

`func(session PreSession, state S, event Event) S` — processes a client
event and returns the new state. Runs inside the command loop; must not
block.

### Bus[E]

Typed pub/sub for cross-handler communication. `Bus.Emit(sess, event)`
accepts `PreSession` directly — no type-assert needed in `Handle`.
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
`Observe(val, session, fn)` subscribes a session — the initial value and
subscription happen atomically within a single session command to prevent
stale overwrites.

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

The `cmds` channel is buffered (default 64, configurable via
`Limits.CmdBufferSize`). When full, a short-lived goroutine delivers the
command instead of blocking the caller. This prevents cross-session
deadlocks during broadcast storms. The first overflow per session logs at
`slog.Warn`; subsequent overflows log at `dev.Debug`.

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
  continue. On reconnect: transport swapped, full re-render sent, URL and
  title replayed.
- **Destroyed**: context cancelled, loop exits, timers stopped.

## Event pipeline

When a client event arrives, `exec()` in `loop.go` runs:

1. Track activity — update timestamp, reset idle timer
2. Snapshot state — store atomically for concurrent `State()` readers
3. Handle — call the handler, get new state
4. Drain effects — collect buffered Toast/Signal/Navigate calls
5. Equality check — skip render if `Equal` says state unchanged
6. Render — build a new node tree
7. Diff — compare with the previous tree via fluent-jit
8. Send — serialise patches + effects, push to the client

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

Register subscriptions in `OnConnect`, not Handle. Subscriptions are
cleaned up automatically when the session is destroyed.

## Key files

| File | Purpose |
|------|---------|
| `config.go` | Config, Timeouts, Limits, Client, Security structs |
| `handler.go` | Handler — session pools, routing, transport upgrade |
| `session.go` | Session struct, enqueue, enqueueFx, drainFx, logOverflow |
| `loop.go` | Command loop (run), exec pipeline, readTransport, send |
| `methods.go` | Session methods — State, Update, Close, Toast, Navigate, Signal, Push |
| `handle.go` | HandleFunc type, middleware chain |
| `serve.go` | HTTP handler (ServeHTTP), session creation, reattach |
| `bus.go` | Bus — typed pub/sub with atomic reads and copy-on-write |
| `group.go` | Group — session pool with Broadcast/BroadcastOthers |
| `value.go` | Value — shared observable state with Bus internally |
| `observe.go` | Observe — atomic subscribe + initial value delivery |
| `emit.go` | On — subscribe a session to a Bus with sender filtering |
| `transport.go` | Transport interface |
| `page.go` | PageConfig, stateless page handler |
| `effects.go` | Effects struct — buffers Toast, Signal, Navigate, etc. |
| `render.go` | Render helpers, tetherBody (root div with data attributes) |
| `asset.go` | Embedded asset serving with content-hashed URLs |
| `listen.go` | ListenAndServe — signal trapping, graceful shutdown |
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

## Conventions

- British spelling in comments, docs, and strings
- Short receiver names (`s` for Session, `b` for Bus, `g` for Group, `h` for Handler/Harness)
- `atomic.Value` + copy-on-write for lock-free reads on shared collections
- Effects are buffered during Handle and merged atomically with the diff
- `dev.Debug` for debug-only logging; `slog.Warn`/`slog.Error` for production
- `context.AfterFunc` for automatic cleanup on context cancellation
