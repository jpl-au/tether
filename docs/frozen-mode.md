# Frozen mode

Frozen mode releases all session memory when a client disconnects.
Instead of keeping state and the command loop alive during the
reconnect window, the session persists state to the SessionStore
and shuts down. On reconnect, state is loaded from the store and
a fresh session is started.

## When to use it

Enable frozen mode when sessions do not need background processing
during disconnect. If your sessions use timers, broadcasts, or
`Update()` calls that fire while disconnected, frozen mode will
discard those — use the default (non-frozen) disconnect behaviour
instead.

Frozen mode is ideal for:

- Applications where sessions are interactive only (no background
  tickers or server-initiated updates while disconnected)
- High session density deployments where disconnected sessions
  should cost near-zero memory
- Environments with long reconnect windows where keeping state
  in memory is wasteful

## Configuration

```go
tether.Live(tether.LiveConfig[State]{
    // ... Render, Handle, etc.

    SessionStore:       myStore,    // required — state must persist somewhere
    FreezeOnDisconnect: true,

    // OnRestore fires when a frozen session is thawed. Use it to
    // re-establish runtime resources (rejoin groups, restart timers).
    // Falls back to OnConnect when nil.
    OnRestore: func(sess *tether.LiveSession[State]) {
        // Rejoin groups, restart watchers, etc.
    },
})
```

`FreezeOnDisconnect` requires a `SessionStore`. If the store is nil,
the framework logs a warning at startup and disables freeze:

```
WARN tether: FreezeOnDisconnect requires a SessionStore — frozen mode disabled because there is nowhere to persist state
```

## Lifecycle

### Disconnect (freeze)

```
1. Transport closes
2. DiffStore.Save(snapshots)     — if configured
3. SessionStore.Save(state, ttl) — serialise S + metadata
4. OnDisconnect fires
5. Release S (zero value) and differ (nil)
6. Set status to Frozen
7. Exit command loop
```

The session remains in the disconnected pool as a lightweight stub.
The reconnect timer keeps running — if it fires before the client
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
reuses the existing session stub — preserving the session ID,
endpoint, user-agent, and context.

### Commands while frozen

Commands (`Update`, `Broadcast`, `Signal`, `Toast`, etc.) sent to a
frozen session are silently discarded. The command loop has exited
and there is no channel to receive them. This is the key trade-off
of frozen mode — background processing stops during disconnect.

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
session clears both on thaw — the differ is rebuilt from a fresh
render, and state is loaded from the SessionStore.

### Groups

Groups are left on disconnect (via `OnDisconnect`) and rejoined on
thaw (via `OnRestore` or `OnConnect`, plus `LiveConfig.Groups`
auto-join). The framework handles auto-join groups automatically
during thaw.

### Watchers

Watchers are re-subscribed during thaw, before `OnRestore` fires.
Any values or bus events that arrived while frozen are lost — the
session picks up the current state from its next render.

### Codec

The `SessionCodec` (default CBOR) serialises state `S` for the
store. The same codec is used for freeze, crash recovery, and
graceful shutdown persistence. See [session-store](session-store.md)
for codec details.

---

[← Back to documentation](../README.md#documentation)
