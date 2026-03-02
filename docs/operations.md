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

`ListenAndServe` handles shutdown automatically — draining sessions on
SIGINT or SIGTERM, then force-closing after the grace period:

```go
h := poly.New(poly.Config[State]{
    // ...
    Timeouts: poly.Timeouts{
        ShutdownGrace: 15 * time.Second, // default: 10s
    },
})

h.ListenAndServe("") // PORT env var, then :8080
```

A second Ctrl+C during shutdown forces an immediate exit.

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
poly.New(poly.Config[State]{
    DevMode: true,
    // ...
})
```

Or set the `POLY_DEV` environment variable without changing code:

```bash
POLY_DEV=1 go run .
```

Dev mode does the following:

1. **No service worker** — unregisters any existing worker and skips registration, so you always get fresh assets
2. **Graceful reconnect + reload** — when the server goes away, the page stays visible with a "Reconnecting…" bar. Once the server comes back, the page reloads to pick up fresh code
3. **No caching** — sets `Cache-Control: no-store` on all responses
4. **Debug logging** — the default logger uses DEBUG level (when no Logger is provided)
5. **Visual flash** — morphed DOM elements flash with a blue outline
6. **Console logging** — events, patches, and morphs are logged to the browser console

The `DevMode` bool takes precedence. When it's false (the default), the `POLY_DEV` environment variable is checked as a fallback.

### Logger format

By default the framework creates a text logger. Set `LogJSON: true` for structured JSON output:

```go
poly.New(poly.Config[State]{
    LogJSON: true,
    // ...
})
```

## Error reporting

Track client-side errors without browser dev tools:

```js
Poly.onError = function(err) {
    // err.type: "parse", "fetch", "worker", "push", "indexeddb", "render"
    // err.message: human-readable description
    fetch("/errors", {
        method: "POST",
        body: JSON.stringify(err)
    });
};
```

When set, `Poly.onError` is called for every error and warning the JS runtime encounters. When not set, warnings are logged to `console.warn` and silent errors (parse failures, IndexedDB issues) remain silent.

## Structural change diagnostics

When a structural change triggers a root morph, the server logs a warning with details:

```
WARN structural change, sending root morph session=abc change="key 'help' added" bytes=15234
     tip="wrap conditional elements in a keyed container to scope this morph"
```

This tells you exactly what changed and how to avoid the cost. Wrapping conditional elements in a stable keyed container keeps morphs scoped instead of full-page.

For production telemetry, use the `OnStructuralChange` callback:

```go
poly.New(poly.Config[State]{
    OnStructuralChange: func(s *poly.Session[State], c poly.StructuralChange) {
        metrics.Counter("structural_changes").Inc()
    },
    // ...
})
```
