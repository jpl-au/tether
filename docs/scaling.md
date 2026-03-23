# Scaling

## Architecture model

Tether is a **stateful, server-driven** framework. Each session holds
its state, diff engine, and command loop in server memory for the duration of
the connection. This is fundamentally different from stateless REST/GraphQL
APIs and has direct implications for scaling.

## Per-session overhead

Each active session consumes:

- **2 goroutines** - the command loop (`run`) and the transport reader
  (`readTransport`)
- **2 buffered channels** - `cmds` and `fxCh`, each sized to
  `Limits.CmdBufferSize` (default 64)
- **The state `S`** - developer-defined, typically a struct
- **The diff engine** - holds a copy of the previous render tree for
  diffing. Memory scales linearly with the number of Dynamic-keyed elements

Disconnected sessions waiting for reconnect retain all of the above except
the transport reader goroutine. When a DiffStore is configured, differ snapshot
data is offloaded to external storage during disconnect, reducing memory
usage for disconnected sessions. When a SessionStore is configured, application
state is also persisted for crash recovery - see
[session-store](session-store.md).

## Vertical scaling

The framework is optimised for vertical scaling. A single server can handle
thousands of concurrent sessions - goroutines are cheap, and the command loop
is lock-free. Profile with `net/http/pprof` to identify bottlenecks under
load.

## Horizontal scaling

Because sessions are in-memory, horizontal scaling requires **sticky sessions**
(session affinity) at the load balancer. A client must always reconnect to
the same server node to reclaim its state.

If a server node crashes, all sessions on that node are lost. Clients
reconnect and receive a fresh `InitialState` - any unsaved ephemeral UI
state is gone.

`Bus` and `Group` are **node-local**. `Bus.Publish` only reaches subscribers
on the same process. To broadcast across nodes (chat, live updates),
bridge the Bus with an external message broker (Redis Pub/Sub, NATS,
or similar):

```go
// Subscribe to an external source and publish locally.
func bridgeBus(ctx context.Context, bus *tether.Bus[Message]) {
    sub := redis.Subscribe(ctx, "messages")
    for msg := range sub.Channel() {
        var m Message
        json.Unmarshal([]byte(msg.Payload), &m)
        bus.Publish(m)
    }
}
```

## Capacity planning

| Setting | Default | Guidance |
|---------|---------|----------|
| `MaxSessions` | 0 (unlimited) | **Set this in production.** Caps total sessions (pending + active + disconnected) |
| `MaxPending` | 128 | Caps pre-warmed sessions awaiting transport connection |
| `CmdBufferSize` | 64 | Increase if `BufferOverflow` diagnostics are frequent |

Use the [health check](operations.md#health-check) endpoint to monitor pool sizes and
feed them into your load balancer's readiness probe.

---

[← Back to documentation](../README.md#documentation)
