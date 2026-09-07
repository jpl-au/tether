# Frozen mode

Frozen mode releases application state and engine memory when a client disconnects.
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
Effects raised after freeze are discarded. Effects already held before
freeze can accompany same-process thaw; crash recovery restores only the
state and metadata persisted by SessionStore.

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
2. Stop accepting HTTP events; apply already acknowledged commands
3. DiffStore.Save(snapshots) - if configured
4. SessionStore.Save(state, ttl) - serialise S + metadata
5. Set status to Frozen and cancel On/Observe subscriptions
6. Publish in the disconnected pool, then call OnDisconnect
7. Release S (zero value) and differ (nil), then exit the command loop
```

If state persistence fails, the session keeps its state and running loop.
HTTP event POSTs return `503` with `Retry-After` while freezing or frozen;
they cannot be acknowledged into an abandoned command queue.

The session remains in the disconnected pool as a lightweight stub.
The reconnect timer keeps running - if it fires before the client
returns, the session is destroyed and its stores, timers and group
membership are cleaned up. The maximum session lifetime still applies.

### Reconnect (thaw)

```
1. Client reconnects with session ID
2. Framework finds frozen session in disconnected pool
3. Wait for the previous command loop and its cleanup to finish
4. Load and decode state S from the envelope
5. Rebuild engine, queues, subscription lifetime and idle timer
6. Start the command loop with catch-up and obsolete-store cleanup queued
7. Mount components, subscribe watchers
8. OnRestore fires (or OnConnect as fallback)
9. Join groups
10. Start transport reader
```

The thaw path is similar to crash recovery (`restoreSession`) but
reuses the existing session stub - preserving the session ID,
endpoint, user-agent, and context.

### Commands while frozen

Commands (`Update`, `Broadcast`, `Signal`, `Toast`, etc.) sent to a
frozen session are discarded with a `CommandDiscarded` diagnostic. The command loop has exited
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
used by command acceptance to reject work when no loop can execute it.
`State()` always reads an atomic snapshot, which is zero while frozen.

## Interaction with other features

### DiffStore

DiffStore and SessionStore are independent. Both save on disconnect.
DiffStore saves differ snapshots (memory optimisation); SessionStore
saves application state (crash recovery and freeze). A frozen
session clears both on thaw - the differ is rebuilt from a fresh
render, and state is loaded from the SessionStore.

### Groups

Automatic groups keep membership until permanent destruction. Broadcasts
to frozen members are discarded. Manual membership can be removed in
`OnDisconnect` and restored on thaw; automatic joins are idempotent.

### Watchers

`On` and `Observe` subscriptions end on freeze and survive ordinary
disconnects. Watchers are re-subscribed before `OnRestore` on thaw.
Imperative subscriptions must be recreated by that callback (or its
`OnConnect` fallback). `Observe` reads the current shared Value again;
bus events published while frozen are not replayed.

### Components and goroutines

Mounts run again on thaw and crash recovery, but not on ordinary
reattachment. `Session.Go` uses the transport context and stops on loss of
the attachment. Ordinary reattachment does not rerun `OnConnect` or restart
that work. Use `Session.Context()` for work that must survive transport loss.

### Codec

The `SessionCodec` (default CBOR) serialises state `S` for the
store. The same codec is used for freeze, crash recovery, and
graceful shutdown persistence. See [session-store](session-store.md)
for codec details.

---

[← Back to documentation](../README.md#documentation)
