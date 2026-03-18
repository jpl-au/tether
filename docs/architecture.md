# Architecture

## Core concepts

tether keeps server state and browser DOM in sync over a persistent connection. The core loop is:

1. **State** — a Go value (typically a struct) owned by a single session
2. **Render** — a pure function that builds a node tree from state
3. **Diff** — the engine compares consecutive renders and produces patches
4. **Send** — patches are serialised and pushed to the browser, which morphs the DOM in place

Each browser tab gets its own session with its own state, its own goroutine, and its own diff engine. There are no mutexes in the hot path — all state mutations are serialised through a command loop.

### Three libraries

| Library | Role |
|---------|------|
| [fluent](https://github.com/jpl-au/fluent) | Structural representation of HTML — composable node trees |
| [fluent-jit](https://github.com/jpl-au/fluent-jit) | Diff engine — compares two node trees and produces patches or morphs |
| **tether** | Session management, transport, wire protocol, and the command loop that ties everything together |

fluent builds the tree. fluent-jit diffs it. tether orchestrates the lifecycle.

## Request lifecycle

### 1. Initial GET — pre-warming

When the browser requests a page, the handler creates state and renders HTML before any transport is connected:

```
Browser                         Server
  │                               │
  │  GET /dashboard               │
  │ ─────────────────────────────>│
  │                               │  InitialState(r) → state
  │                               │  OnNavigate(state, params) → state
  │                               │  Render(state) → node tree
  │                               │  Differ.Render(tree) → HTML
  │                               │  Generate session ID
  │                               │  Store in pending pool
  │  <html>...</html>             │
  │ <─────────────────────────────│
```

The session ID is embedded as a data attribute on the root element. The rendered HTML is immediately visible — no loading spinner, no JavaScript needed for the initial paint.

### 2. Client connects

The client JS (`tether.js`) runs on `DOMContentLoaded`:

1. Reads configuration from data attributes on the tether root element
2. Opens a transport connection — WebSocket by default, SSE as fallback
3. Passes the session ID as a query parameter so the server can reclaim the pre-warmed state

### 3. Transport upgrade

The handler checks three pools in priority order:

1. **Disconnected** — a reconnecting client recovers its existing session (state, timers, subscriptions all preserved)
2. **Pending** — the normal path after a page load; claims the pre-warmed state and diff engine
3. **Fresh** — fallback for direct transport connections without a prior GET; creates everything from scratch

Once a session is claimed or created:

1. The command loop starts: `go sess.run()`
2. `OnConnect` fires — set up subscriptions, join groups, start background work
3. Transport reading begins: `go sess.readTransport(sess.events)`

`OnConnect` runs after the loop starts but before transport reading, so `State()`, `Update()`, `On()`, `Observe()`, and all side-effect methods are safe to call. Client events are not processed until `OnConnect` returns, guaranteeing that subscriptions are in place before the first user interaction arrives.

## The command loop

Every session has a single goroutine that processes three channels:

```go
for {
    select {
    case ev := <-s.events:   // client events from the transport
        s.exec(ev)

    case cmd := <-s.cmds:    // commands from Update, Broadcast, Observe, etc.
        s.runCmd(cmd)

    case fn := <-s.fxCh:     // effects arriving outside of Handle
        s.sendFx(fn)

    case <-s.ctx.Done():     // session destroyed
        return
    }
}
```

All state mutations happen inside this goroutine. `Session.Update()` enqueues a closure on the `cmds` channel; the loop picks it up and runs it. No mutex, no data race, no deadlock.

The `cmds` channel is buffered (default 64, configurable via `Limits.CmdBufferSize`). When the buffer is full — typically during broadcast storms — commands overflow to short-lived goroutines rather than blocking the caller. This prevents cross-session deadlocks where two sessions broadcast to each other simultaneously.

## Event pipeline

When a client event arrives, `exec()` runs the full pipeline:

```
1. Track activity      — update timestamp, reset idle timer
2. Snapshot state      — capture s.state atomically for concurrent readers
3. Component dispatch  — if LiveConfig.Components matches the event prefix, route to the component
4. Handle              — if no component matched, call the page handler
5. Drain effects       — collect buffered Toast/Signal/Navigate calls
6. Equality check      — skip render if Equal says state is unchanged
7. Render              — build a new node tree from the new state
8. Diff                — compare with the previous tree
9. Send                — serialise patches + effects and push to the client
```

Component dispatch (step 3) runs before Handle so that mounted components are self-contained — the application's Handle never sees events meant for a component. Navigate events bypass component dispatch because they always need the `OnNavigate` chain.

### Effect buffering

Side effects called during Handle (`sess.Toast()`, `sess.Signal()`, `sess.Navigate()`, etc.) are not sent immediately. They are buffered on the effects channel and drained after Handle returns. The effects are merged into the same update message as the diff, so the client receives state changes and side effects in a single frame — no flicker, no race.

Effects called outside Handle (from `Session.Go` goroutines, timers, or broadcast callbacks) are sent as standalone updates.

### State snapshots

`Session.State()` uses a fast path when called from within Handle or a goroutine it spawned: it returns an atomic snapshot captured before Handle started, avoiding a channel round-trip that would deadlock the loop. Outside Handle, `State()` routes through the command channel for a consistent read.

## Session pools

Sessions move through three pools managed by the Handler:

```
                    Pending
                  (pre-warmed)
                      │
          transport connects
                      │
                      ▼
                    Active ◄──── reattach (non-frozen)
                 (connected)        ▲
                      │             │
          transport closes          │
                      │             │
                      ▼             │
                 Disconnected ──────┘
              (waiting to reconnect)
                      │
                      ├── FreezeOnDisconnect ──► Frozen ──► thaw ──► Active
                      │                       (zero memory)    │
           reconnect timeout fires              reconnect timeout fires
                      │                                        │
                      ▼                                        ▼
                  Destroyed                                Destroyed
```

**Pending** sessions are created during the initial GET and stored until the transport connects. If the browser never connects (tab closed before JS loads), a cleanup goroutine removes them after `Timeouts.Pending` (default 30s).

**Active** sessions have a connected transport and a running command loop.

**Disconnected** sessions have lost their transport but remain alive for `Timeouts.Reconnect` (default 30s). The command loop keeps running — `Update`, `Broadcast`, and timer callbacks continue to modify state. When a DiffStore is configured, differ snapshots are saved to external storage on disconnect and cleared from memory, reducing per-session overhead during the reconnect window. When a SessionStore is configured, application state `S` and session metadata are saved for crash recovery. When the client reconnects to the same node, the session is reattached: the transport is swapped, store entries are deleted (Render re-seeds the differ), a full re-render is sent to catch the client up, and the browser's URL and title are replayed (they live outside the DOM and would otherwise desync). When the client reconnects after a server restart (crash recovery), the framework restores the session from the SessionStore, fires `OnRestore` (or `OnConnect` as fallback), and sends a full update.

**Frozen** sessions are disconnected sessions with `FreezeOnDisconnect` enabled. Instead of keeping the command loop running, the session persists state `S` to the SessionStore, releases state and the differ from memory, and exits the command loop. The session becomes a lightweight stub holding only its ID, endpoint, and metadata. Commands and effects sent to a frozen session are silently discarded. On reconnect, the framework loads state from the SessionStore, rebuilds the differ, starts a fresh command loop, and fires `OnRestore` (or `OnConnect` as fallback). See [frozen mode](frozen-mode.md) for details.

## Transport abstraction

The `Transport` interface has three methods:

```go
type Transport interface {
    Send(data []byte) error
    ReceiveEvent() (Event, error)
    Close() error
}
```

Two implementations ship with the framework:

| Transport | Server → Client | Client → Server | Keep-alive |
|-----------|----------------|-----------------|------------|
| **WebSocket** (`ws` package) | Text frames | Text frames | Protocol ping/pong |
| **SSE + POST** (`sse` package) | `text/event-stream` | Individual HTTP POST requests | Heartbeat comments |

The client JS tries WebSocket first and falls back to SSE automatically. Set `LiveConfig.Mode` to force one or the other.

SSE heartbeats (`:\n\n` comment lines) are sent at `Timeouts.Heartbeat` (default 20s) to prevent intermediate proxies from closing idle connections.

## Update protocol

The server sends JSON messages containing any combination of:

| Field | Purpose |
|-------|---------|
| `patches` | Targeted content updates — each patch carries a Dynamic key and new HTML |
| `morphs` | Structural DOM changes — the client applies them via idiomorph, preserving focus and scroll |
| `url` | Browser URL update (pushState or replaceState) |
| `title` | Document title |
| `toast` | Global notification |
| `flash` | Selector-targeted notification |
| `signals` | Reactive values pushed to bound elements |
| `announce` | Screen-reader text via aria-live region |
| `eventID` | Echo of the triggering event for client-side de-duplication |

### Patches vs morphs

**Patches** are the common case. When the diff engine finds that a Dynamic-keyed element's HTML changed, it sends a patch with the key and the new content. The client finds the element by key and replaces its innerHTML. Fast and targeted.

**Morphs** are the fallback. When the set of Dynamic keys changes between renders (keys added, removed, or reordered), the diff engine cannot produce targeted patches. Instead it sends a full morph — the complete HTML of the affected subtree (or root). The client applies it via idiomorph, which preserves form state, focus, and scroll position. Correct but heavier.

In practice, stable key sets produce patches on every render. Morphs only occur on structural changes like navigation between pages with different layouts. See [server updates](server-updates.md#stable-key-sets) for how to keep key sets stable.

## Cross-session communication

Three primitives for sharing state across sessions, covered in detail in the [broadcasting guide](broadcasting.md):

| Primitive | Parameterised on | Use case |
|-----------|-----------------|----------|
| **Group** | State type | Broadcast state mutations to sessions of the same handler |
| **Bus** | Event type | Discrete domain events across handlers (chat messages, notifications) |
| **Value** | Value type | Shared observable state all sessions should track (online count, config) |

All three use lock-free reads (atomic.Value) and copy-on-write for writes, so broadcasting never blocks the command loop.

---

[← Back to documentation](../README.md#documentation)
