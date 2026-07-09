# Configuration

## App

`tether.App` holds application-wide configuration shared across handlers. Pass it as the first argument to `tether.Stateful` and `tether.Stateless`.

The zero value provides sensible defaults: WebSocket as the primary
transport, SSE as the fallback, 10-second shutdown grace, and 128
max pending sessions. Override fields as needed:

```go
app := tether.App{
    DevMode:       true,
    WireFormat:    wire.CBOR,    // compact binary encoding (default: wire.JSON)
    MaxSessions:   500,
    ShutdownGrace: 15 * time.Second,
    Assets:        []*tether.Asset{assets},
    Security:      tether.Security{TrustedOrigins: []string{"https://example.com"}},
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DevMode` | `bool` | false | Development mode (or set `TETHER_DEV=1`). See [operations](operations.md#dev-mode) |
| `ShutdownGrace` | `time.Duration` | 10s | Grace period for `ListenAndServe` shutdown. Also used as TTL when saving session state to the SessionStore during shutdown |
| `MaxSessions` | `int` | 0 (unlimited) | Maximum concurrent sessions (pending + active + disconnected). **Set this in production** |
| `MaxPending` | `int` | 128 | Maximum pre-warmed sessions awaiting transport connection |
| `Upgrade` | `func(w, r) (Transport, error)` | `ws.Upgrade()` | Primary transport. Per-handler overrides on `StatefulConfig` take precedence |
| `Fallback` | `func(w, r) (Transport, error)` | `sse.Upgrade()` | Secondary transport. Per-handler overrides on `StatefulConfig` take precedence |
| `Logger` | `*slog.Logger` | auto | When nil, creates a text handler at INFO (DEBUG in DevMode). The dev package logger is scoped to the framework and never touches the process-wide slog default |
| `Assets` | `[]*Asset` | nil | Asset collections (embedded or filesystem) - auto-served with content-hashed URLs |
| `Client` | `Client` | | Browser-side settings (debounce, transitions, flash duration, etc.) |
| `WireFormat` | `wire.Format` | `wire.JSON` | Default wire encoding for all handlers. Per-handler override via `StatefulConfig.WireFormat` takes precedence |
| `Security` | `Security` | | CSRF protection and session binding settings |

---

## StatefulConfig

`tether.StatefulConfig[S]` configures a handler. `InitialState`, `Render`, and `Handle` are required. Everything else has sensible defaults - including transports, which default to WebSocket with SSE fallback.

```go
tether.Stateful(app, tether.StatefulConfig[State]{
    InitialState: func(r *http.Request) State { return State{} },
    Render:       render,
    Handle:       handle,
})
```

### Core

| Field | Type | Description |
|-------|------|-------------|
| `InitialState` | `func(*http.Request) S` | Creates initial state per connection |
| `Render` | `func(S) node.Node` | Builds the node tree from state |
| `Handle` | `func(Session, S, Event) S` | Processes events, returns new state |
| `Middleware` | `[]Middleware[S]` | Wraps Handle with cross-cutting behaviour |
| `OnNavigate` | `func(Session, S, Params) S` | Handles URL navigation and initial load. Redirects via `Navigate()` are resolved inline (no client round-trip). |
| `Layout` | `func(S, node.Node) node.Node` | Wraps the tether root in a full HTML document |
| `Equal` | `func(a, b S) bool` | Skips render when state is unchanged |

### Transport

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Upgrade` | `func(w, r) (Transport, error)` | `ws.Upgrade()` | Primary transport. Inherits from `App`, then falls back to built-in default |
| `Fallback` | `func(w, r) (Transport, error)` | `sse.Upgrade()` | Secondary transport. Inherits from `App`, then falls back to built-in default |
| `Mode` | `mode.Transport` | `mode.Both` | Which transports to accept |

Mode constants: `mode.HTTP`, `mode.WebSocket`, `mode.ServerSentEvents`, `mode.Both`.

### Lifecycle

| Field | Type | Description |
|-------|------|-------------|
| `OnConnect` | `func(*StatefulSession[S])` | Called when a session connects |
| `OnDisconnect` | `func(*StatefulSession[S])` | Called when the transport closes (temporary disconnect or permanent destruction) |
| `OnStructuralChange` | `func(*StatefulSession[S], StructuralChange)` | Called when Dynamic keys change between renders |
| `OnNoPatch` | `func(*StatefulSession[S], NoPatch)` | Called when a render cycle produces no patches |
| `Groups` | `[]*Group[S]` | Groups the session auto-joins on connect |
| `Watchers` | `[]Watcher[S]` | Declarative subscriptions to Value and Bus |
| `Components` | `[]ComponentMount[S]` | Declarative component mounts - events are dispatched by prefix |

`StructuralChange` reports what changed when the diff engine falls back to a root morph:

| Field | Type | Description |
|-------|------|-------------|
| `Added` | `[]string` | Keys in the new tree but not the old |
| `Removed` | `[]string` | Keys in the old tree but not the new |
| `Reordered` | `bool` | Same keys but different order |
| `Bytes` | `int` | Size of the re-rendered HTML sent as a root morph |

`NoPatch` describes a render cycle that produced no DOM changes:

| Field | Type | Description |
|-------|------|-------------|
| `Source` | `string` | `"update"`, `"navigate"`, or `"event"` |
| `Action` | `string` | Event action; empty for `"update"` source |

When either callback is configured, the framework's own logging for that event is suppressed - the callback controls the output. When nil and DevMode is active, a debug message is logged instead.

### Timeouts

`StatefulConfig.Timeouts` groups per-handler duration-based settings:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Idle` | `time.Duration` | 0 (disabled) | Close sessions with no activity for this long. Activity includes client events, Update calls, and server-push effects (Signal, Toast, etc.) |
| `MaxLifetime` | `time.Duration` | 0 (disabled) | Close sessions after this long regardless |
| `Reconnect` | `time.Duration` | 30s | Keep disconnected sessions alive for reconnection |
| `DisableReconnect` | `bool` | false | Destroy sessions immediately on disconnect |
| `Pending` | `time.Duration` | 30s | Wait for browser to claim pre-warmed session |
| `Heartbeat` | `time.Duration` | 20s | Keep-alive interval (SSE comments, WebSocket pings) |
| `DisableHeartbeat` | `bool` | false | Stop transport keep-alive frames |
| `PendingCheck` | `time.Duration` | 10s | How often the background goroutine scans for expired pending sessions |
| `SlowRender` | `time.Duration` | 0 (disabled) | Emit a `SlowRender` diagnostic when render+diff exceeds this duration |
| `Retry` | `time.Duration` | 1s | Initial client reconnection delay |
| `MaxRetry` | `time.Duration` | 30s | Maximum exponential backoff |

### Limits

`StatefulConfig.Limits` groups per-handler capacity constraints:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `CmdBufferSize` | `int` | 64 | Session command channel capacity |
| `MaxEventBytes` | `int64` | 64 KB | Maximum POST event body size |
| `MaxPushSubscriptionBytes` | `int64` | 4 KB | Maximum push subscription body size |
| `MaxStateBytes` | `int64` | 0 (disabled) | Warn when serialised session state exceeds this size (save still proceeds) |
| `MaxNavigateRedirects` | `int` | 5 | Maximum consecutive server-side redirects per navigate event |

### Client

`App.Client` groups browser-side settings:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DefaultDebounce` | `time.Duration` | 300ms | Default input event debounce |
| `TransitionTimeout` | `time.Duration` | 5s | Fallback for CSS `transitionend` |
| `FlashDuration` | `time.Duration` | 5s | Flash message auto-clear duration |
| `ToastDuration` | `time.Duration` | 5s | Toast notification auto-dismiss duration |
| `BackgroundSync` | `bool` | false | Queue failed SSE events in IndexedDB for replay |
| `SyncRetention` | `time.Duration` | 1h | How long queued events survive before expiry |
| `Runtime` | `ClientRuntime` | `Runtime.Default()` | Browser-side client implementation. Use `Runtime.WASM("/static/client.wasm")` for a Go WASM client |

### Extensions

| Field | Type | Description |
|-------|------|-------------|
| `Upload` | `*UploadConfig[S]` | File upload support (see [extensions](extensions.md)) |
| `Push` | `*PushConfig[S]` | Web Push notifications (see [push](push-notifications.md)) |
| `Worker` | `bool` | Enable service worker (auto-enabled by Push) |
| `DiffStore` | `DiffStore` | External persistence for disconnected session snapshots (opt-in, nil by default). See [store](store.md) |
| `SessionStore` | `SessionStore` | External persistence for session state - enables crash recovery and node migration (opt-in, nil by default). See [session-store](session-store.md) |
| `Codec` | `SessionCodec[S]` | Custom serialisation for state `S` (nil = CBOR). Only used when SessionStore is set |
| `OnRestore` | `func(*StatefulSession[S])` | Called instead of OnConnect when a session is restored from the SessionStore |
| `OnPanic` | `func(*StatefulSession[S], error)` | Called when a panic occurs during Handle or Update. When nil (default), the session is destroyed to prevent corrupted state. When set, the session survives and the developer assumes responsibility |
| `OnCommandDropped` | `func(*StatefulSession[S])` | Called when a command is dropped because buffers are full. When nil (default), the session is destroyed to prevent silent drift. When set, the developer handles it |
| `Freeze` | `FreezeMode` | Frozen mode for disconnected sessions. `FreezeWithRestore` requires OnRestore; `FreezeWithConnect` falls back to OnConnect. Zero disables. See [frozen mode](frozen-mode.md) |
| `Protocol` | `protocol.Protocol` | HTTP protocol (default `protocol.Auto` - detects per request). See [transport](transport.md#protocol-awareness) |
| `Memoise` | `bool` | Use the Memoiser engine instead of the Differ. Render functions must use `jit.Memoise` for each Dynamic region. See [engine guide](engine.md#memoiser-opt-in) |
| `WireFormat` | `wire.Format` | Encoding for server-to-client updates (default `wire.JSON`). Overrides `App.WireFormat` for this handler |

### Security

`App.Security` groups CSRF protection and session binding:

| Field | Type | Description |
|-------|------|-------------|
| `TrustedOrigins` | `[]string` | Origins allowed to make state-changing requests |
| `DisableSessionBinding` | `bool` | Disable User-Agent verification entirely (default: enabled) |
| `SessionMatch` | `func(original, reconnect string) bool` | Custom UA comparison. When nil, exact match. See [session binding](security.md#session-binding) |

---

## Handler

`*tether.Handler[S]` implements `http.Handler`.

```go
h := tether.Stateful(app, cfg)
mux.Handle("/app", h)

h.Health()          // HealthStatus{Pending, Active, Disconnected}
h.Drain(ctx)        // stop new sessions, wait for existing
h.Shutdown(ctx)     // close all sessions
```

### ListenAndServe

For single-handler apps, `Handler.ListenAndServe` handles signal trapping, graceful shutdown, and sensible defaults:

```go
h.ListenAndServe("")                      // checks PORT env, defaults to :8080
h.ListenAndServe(":3000")                 // explicit address
h.ListenAndServe("", existingMux)         // mount on an existing mux
h.ListenAndServeTLS("", "cert.pem", "key.pem")  // HTTPS, defaults to :443
```

On `SIGINT` or `SIGTERM`, sessions are drained gracefully (up to `App.ShutdownGrace`, default 10s) before the process exits.

For multi-handler apps, use the package-level `tether.ListenAndServe` which drains and shuts down all handlers concurrently:

```go
tether.ListenAndServe("", mux, wsHandler, sseHandler, swHandler)
```

Any `*Handler[S]` satisfies the `Drainable` interface automatically - no type-assert or adapter needed.

---

[← API reference](api.md)
