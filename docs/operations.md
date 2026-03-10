# Operations

## Health check

Get a snapshot of session pool counts for monitoring, load balancer health checks, or readiness probes:

```go
status := handler.Health()
// status.Pending, status.Active, status.Disconnected
```

`HealthStatus` has JSON tags so you can serve it directly:

```go
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(handler.Health())
})
```

## Graceful shutdown

### Single handler

`Handler.ListenAndServe` handles shutdown automatically — draining sessions
on SIGINT or SIGTERM, then force-closing after the grace period:

```go
h := tether.New(tether.Config[State]{
    // ...
    Timeouts: tether.Timeouts{
        ShutdownGrace: 15 * time.Second, // default: 10s
    },
})

h.ListenAndServe("") // PORT env var, then :8080
```

### Multiple handlers

When several handlers share a single mux, use the package-level
`tether.ListenAndServe`. It drains all handlers concurrently, stops
the HTTP server, then force-closes any remaining sessions:

```go
mux := http.NewServeMux()
mux.Handle("/ws/", wsHandler)
mux.Handle("/sse/", sseHandler)
mux.Handle("/sw/", swHandler)
mux.Handle("/", httpHandler)

tether.ListenAndServe("", mux, wsHandler, sseHandler, swHandler)
```

Both variants trap SIGINT and SIGTERM. A second signal during shutdown
forces an immediate exit.

### Manual shutdown

When using a custom `http.Server`, call `Drain` and `Shutdown` directly:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

handler.Drain(ctx)    // blocks until all sessions disconnect or ctx cancels
handler.Shutdown(ctx) // stops the reaper and releases resources
```

`Drain` rejects new page loads with 503 but allows existing sessions to continue and disconnected clients to reconnect. Per-session timers keep enforcing idle and lifetime limits during the drain period.

## Dev mode

During development, enable dev mode for fast iteration:

```go
tether.New(tether.Config[State]{
    DevMode: true,
    // ...
})
```

Or set the `TETHER_DEV` environment variable without changing code:

```bash
TETHER_DEV=1 go run .
```

Dev mode does the following:

1. **No service worker** — unregisters the service worker scoped to this handler's endpoint and skips registration, so you always get fresh assets. Workers registered by other handlers on the same origin are left alone
2. **Graceful reconnect** — when the server goes away, the page stays visible with a "Reconnecting…" bar. Once the server comes back, the client syncs the current URL so the server re-renders without a page reload. The page is never destroyed on disconnect or reconnect.
3. **No caching** — sets `Cache-Control: no-store` on all responses
4. **Debug logging** — the default logger uses DEBUG level (when no Logger is provided)
5. **Visual flash** — morphed DOM elements flash with a blue outline
6. **Console logging** — events, patches, and morphs are logged to the browser console
7. **Per-session diagnostics** — all session-level debug logging (events, diffs, reconnections, group membership, etc.) is gated behind dev mode via `dev.Debug`. In production with dev mode off, none of this output fires. For structured observability, use `OnStructuralChange` and `OnNoPatch` callbacks instead
8. **Discarded effect warnings** — logs a warning when a handler panic discards buffered side effects (Toast, Signal, Navigate, etc.)

Diagnostics are centralised in the `dev` package. During handler construction, `Config.DevMode` (or `TETHER_DEV`) calls `dev.Enable()` once. After that, all runtime checks — cache headers, the `data-tether-dev` attribute, diagnostic logging — use `dev.Enabled()`. No code threads the `DevMode` bool downstream; everything goes through the `dev` package.

Call sites use `dev.Warn()`, `dev.Debug()`, and `dev.Error()` which silently no-op outside dev mode.

The `DevMode` bool takes precedence. When it's false (the default), the `TETHER_DEV` environment variable is checked as a fallback.

### Logger format

By default the framework creates a text logger. Set `LogJSON: true` for structured JSON output:

```go
tether.New(tether.Config[State]{
    LogJSON: true,
    // ...
})
```

## Error reporting

Track client-side errors without browser dev tools:

```js
Tether.onError = function(err) {
    // err.type: "parse", "fetch", "worker", "push", "indexeddb", "render"
    // err.message: human-readable description
    fetch("/errors", {
        method: "POST",
        body: JSON.stringify(err)
    });
};
```

When set, `Tether.onError` is called for every error and warning the JS runtime encounters. When not set, warnings are logged to `console.warn` and silent errors (parse failures, IndexedDB issues) remain silent.

## Diagnostics bus

`Handler.Diagnostics` is a typed event bus (`Bus[Diagnostic]`) for framework-level
signals. The framework is quiet by default — `slog` is only used for panics as a
critical safety net. All other operational signals flow exclusively through this bus.

```go
h.Diagnostics.Subscribe(ctx, func(d tether.Diagnostic) {
    switch d.Kind {
    case tether.HandlerPanic:
        alerting.Critical(d.SessionID, d.Err)
    case tether.TransportError:
        log.Warn("transport", "session", d.SessionID, "err", d.Err)
    case tether.BufferOverflow:
        metrics.Inc("tether.overflow")
    case tether.CommandDropped:
        metrics.Inc("tether.dropped")
    }
})
```

Use `SubscribeAsync` for callbacks that perform I/O (database writes, HTTP calls):

```go
h.Diagnostics.SubscribeAsync(ctx, func(d tether.Diagnostic) {
    if d.Kind == tether.HandlerPanic {
        alerting.Critical(d.SessionID, d.Err)
    }
})
```

### Diagnostic kinds

| Kind | Meaning |
|------|---------|
| `TransportError` | Failure reading from or writing to the transport. Normal disconnects (io.EOF) are not emitted |
| `EncodeError` | JSON serialisation failure — usually an unencodable type in state or render output |
| `BufferOverflow` | Command channel was full; an overflow goroutine was spawned to deliver the command |
| `CommandDropped` | Both the command buffer and the overflow goroutine cap were exhausted — data was lost |
| `HandlerPanic` | Recovered panic inside Handle, Update, or a command callback |
| `UploadError` | Failure in an upload handler callback |
| `SessionBindingFailed` | A reconnect or session claim was rejected because the User-Agent did not match the original |
| `StoreError` | Failure saving or deleting differ snapshots from the configured DiffStore. The Detail field indicates the operation ("save" or "delete"). Store failures are non-fatal — the framework falls back to in-memory behaviour |
| `SessionStoreError` | Failure saving, loading, or deleting session state from the configured SessionStore. The Detail field indicates the operation ("save", "load", "delete", "marshal", "unmarshal", or "envelope"). Non-fatal — the framework continues with in-memory state |

`BufferOverflow` means the system coped (spawned a goroutine). `CommandDropped`
means data was lost — the session is critically overwhelmed. Sustained overflow
usually indicates a blocking `HandleFunc` or a broadcast rate exceeding the
session's processing speed. Increase `Limits.CmdBufferSize` or move slow work
into `LiveSession.Go`.

The overflow goroutine count is capped by a semaphore sized to `CmdBufferSize`,
preventing unbounded goroutine growth under sustained pressure.

## Structural change diagnostics

When the diff engine detects a structural change (Dynamic keys added, removed, or reordered), it falls back to a full root morph. The `OnStructuralChange` callback lets you observe these for logging, metrics, or debugging:

```go
tether.New(tether.Config[State]{
    OnStructuralChange: func(s *tether.LiveSession[State], c tether.StructuralChange) {
        slog.Warn("structural change",
            "session", s.ID(),
            "added", c.Added,
            "removed", c.Removed,
            "bytes", c.Bytes,
        )
    },
    // ...
})
```

When `OnStructuralChange` is nil and DevMode is active, the framework logs a debug message. When DevMode is off and no callback is set, nothing happens — the framework never pushes diagnostic output at developers who haven't opted in.

Wrapping conditional elements in a stable keyed container keeps morphs scoped instead of full-page. See the [Dynamic keys](server-updates.md#stable-key-sets) section for patterns.

## Scaling

### Architecture model

tether is a **stateful, server-driven** framework. Each session holds
its state, diff engine, and command loop in server memory for the duration of
the connection. This is fundamentally different from stateless REST/GraphQL
APIs and has direct implications for scaling.

### Per-session overhead

Each active session consumes:

- **2 goroutines** — the command loop (`run`) and the transport reader
  (`readTransport`)
- **2 buffered channels** — `cmds` and `fxCh`, each sized to
  `Limits.CmdBufferSize` (default 64)
- **The state `S`** — developer-defined, typically a struct
- **The diff engine** — holds a copy of the previous render tree for
  diffing. Memory scales linearly with the number of Dynamic-keyed elements

Disconnected sessions waiting for reconnect retain all of the above except
the transport reader goroutine. When a DiffStore is configured, differ snapshot
data is offloaded to external storage during disconnect, reducing memory
usage for disconnected sessions. When a SessionStore is configured, application
state is also persisted for crash recovery — see
[session-store](session-store.md).

### Vertical scaling

The framework is optimised for vertical scaling. A single server can handle
thousands of concurrent sessions — goroutines are cheap, and the command loop
is lock-free. Profile with `net/http/pprof` to identify bottlenecks under
load.

### Horizontal scaling

Because sessions are in-memory, horizontal scaling requires **sticky sessions**
(session affinity) at the load balancer. A client must always reconnect to
the same server node to reclaim its state.

If a server node crashes, all sessions on that node are lost. Clients
reconnect and receive a fresh `InitialState` — any unsaved ephemeral UI
state is gone.

`Bus` and `Group` are **node-local**. `Bus.Publish` only reaches subscribers
on the same process. To broadcast across nodes (chat, live updates),
bridge the Bus with an external message broker (Redis Pub/Sub, NATS,
or similar):

```go
// Subscribe to an external source and publish locally.
func bridgeBus(ctx context.Context, bus *tether.Bus[Message]) {
    sub := redis.Subscribe(ctx, "messages")
    for msg := range sub.Channel() {
        var m Message
        json.Unmarshal([]byte(msg.Payload), &m)
        bus.Publish(m)
    }
}
```

### Capacity planning

| Setting | Default | Guidance |
|---------|---------|----------|
| `MaxSessions` | 0 (unlimited) | **Set this in production.** Caps total sessions (pending + active + disconnected) |
| `MaxPending` | 128 | Caps pre-warmed sessions awaiting transport connection |
| `CmdBufferSize` | 64 | Increase if `BufferOverflow` diagnostics are frequent |

Use the [health check](#health-check) endpoint to monitor pool sizes and
feed them into your load balancer's readiness probe.
