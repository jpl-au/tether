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

`Handler.ListenAndServe` handles shutdown automatically - draining sessions
on SIGINT or SIGTERM, then force-closing after the grace period:

```go
app := tether.App{
    ShutdownGrace: 15 * time.Second, // default: 10s
}

h := tether.Stateful(app, tether.StatefulConfig[State]{
    // ...
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
app := tether.App{DevMode: true}

tether.Stateful(app, tether.StatefulConfig[State]{
    // ...
})
```

Or set the `TETHER_DEV` environment variable without changing code:

```bash
TETHER_DEV=1 go run .
```

Dev mode does the following:

1. **No service worker** - unregisters the service worker scoped to this handler's endpoint and skips registration, so you always get fresh assets. Workers registered by other handlers on the same origin are left alone
2. **Graceful reconnect** - when the server goes away, the page stays visible with a "Reconnecting…" bar. Once the server comes back, the client syncs the current URL so the server re-renders without a page reload. The page is never destroyed on disconnect or reconnect.
3. **No caching** - sets `Cache-Control: no-store` on all responses
4. **Debug logging** - the default logger uses DEBUG level (when no Logger is provided)
5. **Visual flash** - morphed DOM elements flash with a blue outline
6. **Console logging** - events, patches, and morphs are logged to the browser console
7. **Per-session diagnostics** - all session-level debug logging (events, diffs, reconnections, group membership, etc.) is gated behind dev mode via `dev.Debug`. In production with dev mode off, none of this output fires. For structured observability, use `OnStructuralChange` and `OnNoPatch` callbacks instead
8. **Discarded effect warnings** - logs a warning when a handler panic discards buffered side effects (Toast, Signal, Navigate, etc.)

### Filesystem asset watching

When assets live outside the binary (e.g. `os.DirFS`), set `WatchDir`
on the Asset to enable automatic hash invalidation:

```go
var assets = &tether.Asset{
    FS:       os.DirFS("./static"),
    Prefix:   "/static/",
    WatchDir: "./static",
}
```

The watcher uses `fsnotify` to detect file changes. When a file is
modified, only that file's hash is recomputed. The next request gets
the new hash in the URL, so browsers fetch the updated asset. The
watcher logs events at debug level when dev mode is enabled.

Call `assets.Close()` during graceful shutdown to stop the watcher
goroutine and release the file descriptors.

### Logging architecture

Tether never touches the process-wide `slog` default. All framework
log output flows through the internal `dev` package, which holds a
scoped logger configured during handler construction.

There are two categories of log output:

**Dev-only** (`dev.Warn`, `dev.Debug`): gated behind dev mode. These
help developers catch mistakes early (missing Dynamic keys, discarded
effects, malformed URLs) but would be noise in production. For
production observability, subscribe to `Handler.Diagnostics` instead.

**Safety-net** (`dev.Log().Error`): always fires regardless of dev
mode. Used for panics and critical errors as a last resort - if nobody
is subscribed to the diagnostics bus, the developer still sees
something in their logs. The diagnostics bus is the primary
observability channel; these log calls are the fallback.

All logging is centralised in the `dev` package. During handler
construction, `App.DevMode` (or `TETHER_DEV`) calls `dev.Enable()`
once. `App.Logger` (or a default text logger) is installed via
`dev.SetLogger()`. After that, all runtime checks - cache headers,
the `data-tether-dev` attribute, diagnostic logging - use
`dev.Enabled()`. No code threads the `DevMode` bool downstream;
everything goes through the `dev` package.

The `DevMode` bool takes precedence. When it's false (the default),
the `TETHER_DEV` environment variable is checked as a fallback.

### Logger format

By default the framework creates a text logger. For structured JSON
output, provide a custom logger via `App.Logger`. The logger is
scoped to tether and does not affect the process-wide `slog` default:

```go
app := tether.App{
    Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)),
}

tether.Stateful(app, tether.StatefulConfig[State]{
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
signals. The framework is quiet by default - `slog` is only used for panics as a
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
| `EncodeError` | JSON serialisation failure - usually an unencodable type in state or render output |
| `BufferOverflow` | Command channel was full; an overflow goroutine was spawned to deliver the command |
| `CommandDropped` | Both the command buffer and the overflow goroutine cap were exhausted. By default the session is destroyed to prevent silent client drift. Set `OnCommandDropped` to override |
| `CommandDiscarded` | A command or effect was silently discarded because the session is frozen or destroyed. Happens when code calls Update, Signal, Toast, etc. after disconnection |
| `HandlerPanic` | Recovered panic inside Handle, Update, or a command callback. By default the session is destroyed because state may be corrupted. Set `OnPanic` to override |
| `UploadError` | Failure or recovered panic in an upload handler callback |
| `UploadRejected` | An upload was rejected because its MIME type did not match the `UploadConfig.Accept` list. Detail contains the rejected content type |
| `SessionBindingFailed` | A reconnect or session claim was rejected because the User-Agent did not match the original |
| `StoreError` | Failure saving or deleting differ snapshots from the configured DiffStore. The Detail field indicates the operation ("save" or "delete"). Store failures are non-fatal - the framework falls back to in-memory behaviour |
| `SessionStoreError` | Failure saving, loading, or deleting session state from the configured SessionStore. The Detail field indicates the operation ("save", "load", "delete", "marshal", "unmarshal", or "envelope"). Non-fatal - the framework continues with in-memory state |
| `StateSizeExceeded` | Serialised session state exceeded `Limits.MaxStateBytes`. The save proceeds - this is a warning, not a hard limit. Detail contains the size in bytes |
| `SlowRender` | A render+diff cycle exceeded `Timeouts.SlowRender`. Detail contains the duration. Use this to identify candidates for memoisation |
| `NavigateRedirectLoop` | An OnNavigate handler triggered more consecutive redirects than `Limits.MaxNavigateRedirects` allows |
| `RenderCoalesced` | Multiple commands were batched into a single render-diff-send cycle. Detail contains the batch count. Only fires when the batch size exceeds one |
| `HighMemoiseMissRate` | The memoisation cache miss ratio for a render cycle exceeded `Timeouts.MemoiseMissThreshold`. Usually indicates broken or overly granular cache keys. Only fires when Memoise is enabled |

`BufferOverflow` means the system coped (spawned a goroutine). `CommandDropped`
means the session was critically overwhelmed - by default it is destroyed to
prevent silent client drift. To keep the session alive on drop, set
`StatefulConfig.OnCommandDropped`. Sustained overflow usually indicates a
blocking `HandleFunc` or a broadcast rate exceeding the session's processing
speed. Increase `Limits.CmdBufferSize` or move slow work into
`StatefulSession.Go`.

The overflow goroutine count is capped by a semaphore sized to `CmdBufferSize`,
preventing unbounded goroutine growth under sustained pressure.

## Structural change diagnostics

See [OnStructuralChange](server-updates.md#onstructuralchange---observing-structural-changes) and [OnNoPatch](server-updates.md#onnopatch---observing-empty-render-cycles) in the server updates guide.

## Scaling

See [scaling](scaling.md) for per-session overhead, vertical and horizontal scaling guidance, cross-node broadcasting patterns, and capacity planning.

---

[← Back to documentation](../README.md#documentation)
