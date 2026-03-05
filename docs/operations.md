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
