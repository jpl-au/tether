# Frozen mode

Frozen mode releases all session memory when a client disconnects.
Instead of keeping state and the command loop alive during the
reconnect window, the session persists state to the SessionStore
and shuts down. On reconnect, state is loaded from the store and
a fresh session is started.

## When to use it

Enable frozen mode when sessions do not need background processing
during disconnect. A frozen session has no command loop, so timers,
broadcasts, and `Update()` calls that fire while it is away are
discarded outright and the state they would have produced is lost.

The default (non-frozen) behaviour keeps the loop running, so those
mutations still apply, and the reconnect sends the client everything it
missed - both the state changes and the side effects that described
them (`Toast`, `Flash`, `Announce`, `Signal`, `SetTitle`). See
[architecture](architecture.md#session-pools) for the full contract.
A frozen session has no loop to hold either, so nothing survives except
what the SessionStore persisted.

Frozen mode is ideal for:

- Applications where sessions are interactive only (no background
  tickers or server-initiated updates while disconnected)
- High session density deployments where disconnected sessions
  should cost near-zero memory
- Environments with long reconnect windows where keeping state
  in memory is wasteful

## Configuration

The `Freeze` field accepts a `FreezeMode` value. Zero (the default)
disables freeze entirely.

### FreezeWithRestore (recommended)

Requires `OnRestore` to be set. The developer must re-fetch
authoritative state from the database or other source on thaw. The
framework panics at startup if `OnRestore` is nil.

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    // ... Render, Handle, etc.

    SessionStore: myStore,
    Freeze:       tether.FreezeWithRestore,

    OnRestore: func(sess *tether.StatefulSession[State]) {
        // Re-fetch state from the database, rejoin groups,
        // restart timers. The store snapshot may be stale -
        // always re-fetch authoritative data here.
    },
})
```

### FreezeWithConnect

Falls back to `OnConnect` on thaw. Use this when `OnConnect` already
performs full initialisation and a dedicated `OnRestore` is not needed.
The developer accepts that the restored snapshot may be stale if
domain events occurred while frozen.

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    // ... Render, Handle, etc.

    SessionStore: myStore,
    Freeze:       tether.FreezeWithConnect,

    OnConnect: func(sess *tether.StatefulSession[State]) {
        // Same setup for new and restored sessions.
    },
})
```

### Validation

`Freeze` requires a `SessionStore`. The framework panics at startup
if the store is nil:

```
panic: tether: Freeze requires a SessionStore
```

`FreezeWithRestore` additionally requires `OnRestore`:

```
panic: tether: FreezeWithRestore requires OnRestore - implement OnRestore to re-fetch state, or use FreezeWithConnect to fall back to OnConnect
```

## Lifecycle

### Disconnect (freeze)

```
1. Transport closes
2. DiffStore.Save(snapshots) - if configured
3. SessionStore.Save(state, ttl) - serialise S + metadata
4. OnDisconnect fires
5. Release S (zero value) and differ (nil)
6. Set status to Frozen
7. Exit command loop
```

The session remains in the disconnected pool as a lightweight stub.
The reconnect timer keeps running - if it fires before the client
returns, the session is destroyed and the store entry expires via
its TTL.

### Reconnect (thaw)

```
1. Client reconnects with session ID
2. Framework finds frozen session in disconnected pool
3. SessionStore.Load(id) → state bytes
4. Decode state S from envelope
5. Create fresh differ, render initial tree
6. Rebuild channels, start new command loop
7. Mount components, subscribe watchers
8. OnRestore fires (or OnConnect as fallback)
9. Join groups
10. SessionStore.Delete(id)
11. Start transport reader
```

The thaw path is similar to crash recovery (`restoreSession`) but
reuses the existing session stub - preserving the session ID,
endpoint, user-agent, and context.

### Commands while frozen

Commands (`Update`, `Broadcast`, `Signal`, `Toast`, etc.) sent to a
frozen session are silently discarded. The command loop has exited
and there is no channel to receive them. This is the key trade-off
of frozen mode - background processing stops during disconnect.

## Session status

Sessions have an explicit lifecycle status:

| Status | Meaning |
|--------|---------|
| `Pending` | Created on initial GET, awaiting transport |
| `Active` | Command loop running, transport may be attached |
| `Frozen` | State persisted, memory released, loop exited |
| `Destroyed` | Permanently gone, context cancelled |

The status is stored as an `atomic.Int32` on the session and is
used by `enqueue`, `enqueueFx`, and `State()` to guard against
operating on a session in an unexpected state.

## Interaction with other features

### DiffStore

DiffStore and SessionStore are independent. Both save on disconnect.
DiffStore saves differ snapshots (memory optimisation); SessionStore
saves application state (crash recovery and freeze). A frozen
session clears both on thaw - the differ is rebuilt from a fresh
render, and state is loaded from the SessionStore.

### Groups

Groups are left on disconnect (via `OnDisconnect`) and rejoined on
thaw (via `OnRestore` or `OnConnect`, plus `StatefulConfig.Groups`
auto-join). The framework handles auto-join groups automatically
during thaw.

### Watchers

Watchers are re-subscribed during thaw, before `OnRestore` fires.
Any values or bus events that arrived while frozen are lost - the
session picks up the current state from its next render.

### Codec

The `SessionCodec` (default CBOR) serialises state `S` for the
store. The same codec is used for freeze, crash recovery, and
graceful shutdown persistence. See [session-store](session-store.md)
for codec details.

---

[← Back to documentation](../README.md#documentation)
