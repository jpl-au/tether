# Cluster

Cross-node communication for Bus and Value. When a Cluster is
configured on App, any Bus or Value with a topic name publishes
changes to other nodes and receives their changes automatically.
Groups remain local - cross-node state synchronisation flows through
Bus events.

## Quick start

```go
app := tether.App{
    Cluster: tetheredis.New(rdb),
}

// Clustered bus - events reach all nodes
var messages = tether.NewBus[Message](tether.BusConfig{Topic: "messages"})

// Clustered value - updates reach all nodes
var onlineCount = tether.NewValue(0, "online-count")

// Local bus - no cluster interaction (the default)
var internal = tether.NewBus[InternalEvent]()
```

## How it works

### Bus

When a Bus has a topic and a Cluster is configured:

1. `Publish(event)` delivers to local subscribers, then CBOR-encodes
   the event into an envelope and publishes it to the cluster topic
2. Other nodes receive the envelope, decode it, and deliver to their
   local subscribers
3. The originating node's cluster subscription filters out its own
   messages by node ID, preventing double delivery

`Emit(session, event)` works the same way. The sender's session ID
is included in the envelope so the originating node can still filter
the sender locally.

### Value

When a Value has a topic and a Cluster is configured:

1. `Store(val)` or `Update(fn)` updates locally, then CBOR-encodes
   the new value and publishes it to the cluster topic
2. Other nodes receive the envelope, decode it, and update their
   local Value without re-publishing (preventing infinite loops)
3. Self-filtering by node ID prevents a node from applying its own
   update twice

Value uses last-writer-wins. Concurrent updates from different nodes
converge to whichever message arrives last. This is acceptable for
counters, flags, and presence indicators. For stronger consistency,
use an external source of truth and treat Value as a reactive cache.

### Group

Group stays local. Cross-node state synchronisation should flow
through a clustered Bus. The pattern: emit the cause (a typed event)
to a Bus, and let each node's local WatchBus apply it independently.

```go
// Instead of group.Broadcast across nodes, emit the event:
var chatBus = tether.NewBus[ChatMessage](tether.BusConfig{Topic: "chat"})

// Each node's sessions receive it via WatchBus:
tether.WatchBus(chatBus, func(msg ChatMessage, s State) State {
    s.Messages = append(s.Messages, msg)
    return s
})
```

This works because each node independently applies the event to its
own sessions, respecting per-session state differences.

## Topic naming

Topics are prefixed automatically to prevent collisions:

- Bus topics: `tether:bus:{name}`
- Value topics: `tether:value:{name}`

A Bus and a Value can share the same name (they use different
prefixes). Two Buses with the same topic name will panic at startup -
duplicate topics indicate a configuration error.

Topic names should be short, descriptive, and use lowercase with
hyphens: `"messages"`, `"online-count"`, `"page-views"`.

## Serialisation

Events and values are serialised with CBOR (RFC 8949) via the same
`fxamacker/cbor/v2` library used for session persistence. The same
constraints apply:

- Exported fields only (or fields with `cbor` struct tags)
- No channels, functions, or interface values
- No runtime handles (*sql.DB, *http.Client, etc.)

## Error handling

- **Publish failures are logged, not fatal.** The local operation
  always succeeds. If the cluster is unreachable, remote nodes miss
  the update but catch up on the next event.
- **Subscribe failures panic at startup.** A bus that cannot receive
  remote events is silently broken - better to fail loudly, the same
  way duplicate HTTP routes panic.
- **Duplicate delivery is possible** during broker reconnection.
  Bus subscribers should be idempotent or tolerate duplicates. Value
  is naturally idempotent (last-writer-wins).

## The Cluster interface

Implement this to integrate with your messaging infrastructure:

```go
type Cluster interface {
    Publish(ctx context.Context, topic string, data []byte) error
    Subscribe(topic string, fn func(data []byte)) func()
}
```

Two methods. The framework handles serialisation, self-filtering,
and topic naming. Implementations only move bytes between nodes.

The `tetheredis` subpackage provides a Redis Pub/Sub implementation.

## Configuration

```go
app := tether.App{
    Cluster: tetheredis.New(rdb),
}
```

One field on App. Buses and Values with topic names automatically
participate. Without a Cluster, everything works locally.

---

[← Back to documentation](../README.md#documentation)
