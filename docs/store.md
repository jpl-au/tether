# DiffStore

> For session state persistence (crash recovery, node migration), see
> [SessionStore](session-store.md). DiffStore and SessionStore are
> independent concerns - different data, different lifecycles.

## What it does

Disconnected sessions keep their differ snapshots in process memory while
waiting to reconnect. The `DiffStore` interface lets you move that data to
external storage during the reconnect window, freeing Go memory.

By default (`StatefulConfig.DiffStore` is nil), nothing changes - sessions stay in
memory exactly as they always have. Set `StatefulConfig.DiffStore` to opt in.

## The interface

```go
type DiffStore interface {
    Save(ctx context.Context, id string, data []byte) error
    Load(ctx context.Context, id string) ([]byte, error)
    Delete(ctx context.Context, id string) error
}
```

The `data` is an opaque blob produced by the differ's export method.
DiffStore implementations must not interpret or modify the bytes - the
encoding is an internal detail that may change between framework versions.

Implementations must be safe for concurrent use.

## Lifecycle

```
Active  →  Disconnect  →  Export  →  Save  →  Clear
                                                 ↓
Reconnect  →  Delete  →  Render (re-seeds differ)

Destroy  →  Delete
```

**On disconnect:**

1. The differ's snapshots are exported to `[]byte`
2. `DiffStore.Save(ctx, sessionID, data)` persists the bytes
3. If Save succeeds, the differ is cleared - memory freed
4. If Save fails, nothing is cleared - data stays in the differ, a
   `StoreError` diagnostic is emitted, and the session continues as
   if no store were configured

The save runs before the session enters the disconnected pool, so the
data is persisted before the session becomes visible as reconnectable.

**On reconnect:**

1. `DiffStore.Delete(ctx, sessionID)` removes the stored data
2. The session re-renders from state, which re-seeds the differ
3. A full update is sent to catch the client up

`DiffStore.Load` is not called on reconnect. Re-rendering from state is
simpler and eliminates a failure point.

**On destroy (session expires or server shuts down):**

1. `DiffStore.Delete(ctx, sessionID)` cleans up any stored data

## Writing a DiffStore implementation

The framework ships no implementations - you provide your own. A DiffStore
is a dumb key-value store keyed by session ID. Here is a minimal
in-memory example to show the shape:

```go
type MemoryStore struct {
    mu   sync.Mutex
    data map[string][]byte
}

func NewMemoryStore() *MemoryStore {
    return &MemoryStore{data: make(map[string][]byte)}
}

func (m *MemoryStore) Save(_ context.Context, id string, data []byte) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.data[id] = data
    return nil
}

func (m *MemoryStore) Load(_ context.Context, id string) ([]byte, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.data[id], nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    delete(m.data, id)
    return nil
}
```

In practice you would back this with Redis, SQLite, a filesystem, or
whatever suits your deployment.

## Configuration

```go
h := tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    DiffStore: NewRedisStore(redisClient),
    // ...
})
```

## Load

`DiffStore.Load` is defined on the interface but not called by the framework
today. It exists for tooling, debugging, and potential future
optimisations. If you are writing a DiffStore implementation, implement Load
 -  but know that the framework will not call it during normal operation.

## Failure behaviour

DiffStore failures are non-fatal. The store is an optimisation, not a hard
dependency.

| Failure | Effect |
|---------|--------|
| Save fails | Snapshots remain in the differ's memory. Session continues normally. `StoreError` diagnostic emitted |
| Delete fails | Orphaned data in the store. Non-fatal. `StoreError` diagnostic emitted |

Subscribe to `StoreError` diagnostics for alerting:

```go
h.Diagnostics.Subscribe(ctx, func(d tether.Diagnostic) {
    if d.Kind == tether.StoreError {
        log.Warn("store error",
            "session", d.SessionID,
            "op", d.Detail,
            "err", d.Err,
        )
    }
})
```

## Sizing

The blob size depends on the number of Dynamic keys and the size of the
rendered HTML per key. Pages with many Dynamic regions or large fragments
can produce multi-megabyte blobs. Account for this when choosing storage
backends and setting size limits.

---

[← Back to documentation](../README.md#documentation)
