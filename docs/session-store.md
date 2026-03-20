# SessionStore

## What it does

By default, sessions live entirely in memory. A server restart loses
all state - reconnecting clients get fresh sessions. `SessionStore`
changes this: the framework persists the developer's application state
`S` (plus session metadata) on disconnect and graceful shutdown, and
restores it when a reconnecting client reaches a server that has no
in-memory session.

This enables crash recovery and node migration without the developer
writing any persistence plumbing.

By default (`StatefulConfig.SessionStore` is nil), nothing changes - sessions
are in-memory only. Set `StatefulConfig.SessionStore` to opt in.

## How it relates to DiffStore

These are independent concerns:

| | DiffStore | SessionStore |
|---|---|---|
| **Data** | Opaque differ snapshots | Serialised state `S` + metadata |
| **Purpose** | Memory optimisation | Crash recovery |
| **Save trigger** | On disconnect | On disconnect + graceful shutdown |
| **Load trigger** | Not called by framework | On crash recovery |
| **StatefulConfig field** | `StatefulConfig.DiffStore` | `StatefulConfig.SessionStore` |

A developer may use one without the other, both, or neither.

SessionStore is also required for [frozen mode](frozen-mode.md)
(`FreezeOnDisconnect`), which releases all session memory on
disconnect and restores from the store on reconnect.

## The interface

```go
type SessionStore interface {
    Save(ctx context.Context, id string, data []byte, ttl time.Duration) error
    Load(ctx context.Context, id string) ([]byte, error)
    Delete(ctx context.Context, id string) error
}
```

The `data` is an opaque envelope produced by the framework containing
the serialised state and session metadata. Implementations must not
interpret or modify the bytes.

The `ttl` on Save is a hint - the framework passes the reconnect
window on disconnect, or the shutdown grace period on graceful
shutdown. Implementations may use it for automatic expiry (e.g.
Redis `SETEX`), store it for periodic cleanup, or ignore it entirely.
TTL is a safety net for orphaned data; under normal operation,
`Delete` handles cleanup.

Implementations must be safe for concurrent use.

## Lifecycle

```
Active  →  Disconnect  →  Encode S  →  Wrap envelope  →  Save
                                                            ↓
Crash recovery  →  Load  →  Unwrap  →  Decode S  →  New session
                                                        ↓
                                                    OnRestore / OnConnect
                                                        ↓
                                                    Delete store entry

Same-node reconnect  →  Delete (state is in memory)

Destroy  →  Delete

Graceful shutdown  →  Save all active sessions
```

**On disconnect:**

1. The codec serialises state `S` to bytes (CBOR by default)
2. The framework wraps the bytes in an envelope with session metadata
   (endpoint, URL, title, user-agent)
3. `SessionStore.Save(ctx, id, envelope, ttl)` persists the data
4. TTL matches `Timeouts.Reconnect`

If any step fails, a `SessionStoreError` diagnostic is emitted and
the session continues with in-memory state.

**On same-node reconnect:**

The session is still in memory - state `S` is current. The store
entry is stale (state may have changed via `Update`/broadcasts
during disconnect).

1. `SessionStore.Delete(ctx, id)` removes the stale entry

**On crash recovery (different node or restart):**

A reconnecting client presents a session ID that isn't in any
in-memory pool. The framework checks the SessionStore before
rejecting the reconnect.

1. `SessionStore.Load(ctx, id)` retrieves the envelope
2. Envelope is unwrapped, state `S` is decoded
3. User-agent binding is verified
4. A new session is created with the restored state
5. `OnRestore` fires (or `OnConnect` as fallback)
6. `Render(S)` seeds the differ
7. Full update sent to the client
8. `SessionStore.Delete(ctx, id)` cleans up

**On destroy:**

1. `SessionStore.Delete(ctx, id)` removes any stored data

**On graceful shutdown:**

Before the process exits, the framework saves all active sessions:

1. For each session: encode `S`, wrap envelope, save with TTL
2. TTL matches `Timeouts.ShutdownGrace`

On restart, reconnecting clients recover via the crash recovery path.

## Codec

The framework serialises state `S` using CBOR (RFC 8949) by default.
No configuration, no struct tags, no boilerplate - it works for any
struct with exported fields.

When you need control (encryption, a company-standard format, complex
types), implement `SessionCodec[S]`:

```go
type SessionCodec[S any] interface {
    Marshal(state S) ([]byte, error)
    Unmarshal(data []byte) (S, error)
}
```

Set `StatefulConfig.Codec` to use it. The codec only handles `S` - session
metadata (URL, title, user-agent) is wrapped separately by the
framework.

### Constraints on S (when using default CBOR)

- Exported fields only (or fields with `cbor` struct tags)
- No channels, functions, or interface values
- No runtime handles (`*sql.DB`, `*http.Client`, etc.)

State should be pure data. Runtime handles belong in lifecycle hooks
(`OnConnect`, `OnRestore`), not in the state struct.

## OnRestore

```go
type StatefulConfig[S any] struct {
    OnRestore func(session *StatefulSession[S])
}
```

`OnRestore` fires instead of `OnConnect` for restored sessions. Use
it to re-establish runtime resources: rejoin groups, restart timers,
re-subscribe to buses.

If nil, `OnConnect` fires as a fallback - suitable for apps where
setup is identical for new and restored sessions.

## Configuration

```go
h := tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    SessionStore: myRedisStore,
    // Codec: myCustomCodec,  // optional, defaults to CBOR
    // OnRestore: func(sess *tether.StatefulSession[State]) { ... },
    // ...
})
```

## Failure behaviour

SessionStore failures are non-fatal. The store enables recovery but
is not a hard dependency.

| Failure | Effect |
|---------|--------|
| Save fails | State remains in memory. Session continues normally. `SessionStoreError` diagnostic emitted |
| Load fails | Client gets a fresh session. `SessionStoreError` diagnostic emitted |
| Delete fails | Orphaned data in the store (TTL handles cleanup). `SessionStoreError` diagnostic emitted |
| Codec fails | Same as Save/Load failure - diagnostic emitted, session continues or starts fresh |

Subscribe to `SessionStoreError` diagnostics for alerting:

```go
h.Diagnostics.Subscribe(ctx, func(d tether.Diagnostic) {
    if d.Kind == tether.SessionStoreError {
        log.Warn("session store error",
            "session", d.SessionID,
            "op", d.Detail,
            "err", d.Err,
        )
    }
})
```

## State versioning

CBOR handles missing and extra fields gracefully - new fields get
zero values, removed fields are silently dropped. This covers the
common case of deploying new code while sessions are stored.

For applications that need explicit version control, the
`SessionCodec` is the escape hatch. A codec can embed a version
number in its output and handle migration in `Unmarshal`.

---

[← Back to documentation](../README.md#documentation)
