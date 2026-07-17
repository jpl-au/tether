# Scaling

## Architecture model

Tether is a **stateful, server-driven** framework. Each session holds
its state, diff engine, and command loop in server memory for the duration of
the connection. This is fundamentally different from stateless REST/GraphQL
APIs and has direct implications for scaling.

## Per-session overhead

Each active session consumes:

- **2 goroutines** - the command loop (`run`) and the transport reader
  (`readTransport`). Goroutine stacks start at ~4 KB and grow up to 1 MB.
- **2 buffered channels** - `cmds` and `fxCh`, each sized to
  `Limits.CmdBufferSize` (default 64)
- **Overflow semaphore** - same size as `CmdBufferSize`
- **Per-session timers** - up to 3 (`idle`, `disconnect`, `lifetime`)
- **The state `S`** - developer-defined, typically a struct
- **The diff engine** - holds a copy of the previous render tree for
  diffing. Memory scales linearly with the number of Dynamic-keyed elements

A reasonable baseline estimate is **15-50 KB per active session** before
application state. The lower end is a minimal state struct with a
shallow render tree. The upper end is a complex page with many Dynamic
regions and a large state struct.

### Estimating capacity

Multiply the per-session estimate by your target session count, then add
headroom for application state and GC overhead:

| Sessions | Baseline (at 30 KB each) | With 10 KB state each | Total |
|----------|-------------------------|-----------------------|-------|
| 1,000    | 30 MB                   | 10 MB                 | ~40 MB |
| 10,000   | 300 MB                  | 100 MB                | ~400 MB |
| 50,000   | 1.5 GB                  | 500 MB                | ~2 GB |
| 100,000  | 3 GB                    | 1 GB                  | ~4 GB |

GC pressure increases with heap size and pointer-heavy data (differ
trees, subscriber maps). Profile with `net/http/pprof` under realistic
load to measure actual memory consumption rather than relying on
estimates alone.

### Reducing memory for disconnected sessions

Disconnected sessions waiting for reconnect retain all of the above
except the transport reader goroutine. Three strategies reduce this:

- **DiffStore** - offloads differ snapshots to external storage on
  disconnect. The session retains state but releases the diff engine.
  See [store](store.md).
- **SessionStore** - persists application state for crash recovery
  and node migration. See [session-store](session-store.md).
- **Freeze mode** - the most aggressive option. On disconnect, state
  and the diff engine are serialised to the SessionStore and released
  from memory. The session stub retains only metadata (ID, endpoint,
  user-agent). Memory drops to a few hundred bytes per frozen session.
  On reconnect, state is restored from the store and a new command
  loop starts. See [frozen-mode](frozen-mode.md).

For deployments with many disconnected sessions (mobile apps, flaky
networks), freeze mode is the recommended approach. It turns the
memory ceiling from "total sessions" into "active sessions."

## Vertical scaling

The framework is optimised for vertical scaling. A single server can handle
thousands of concurrent sessions - goroutines are cheap, and the command loop
is lock-free. Profile with `net/http/pprof` to identify bottlenecks under
load.

## Horizontal scaling

Because sessions are in-memory, horizontal scaling requires **sticky sessions**
(session affinity) at the load balancer. A client must always reconnect to
the same server node to reclaim its state.

If a server node crashes, all sessions on that node are lost unless a
`SessionStore` is configured. Without one, clients reconnect and receive
a fresh `InitialState` - any unsaved state is gone. With one, state is
restored from the store on reconnect, and can even be reclaimed on a
different node (node migration). See [session-store](session-store.md).

`Bus`, `Value`, and `Group` are **node-local** by default - `Bus.Publish`
only reaches subscribers on the same process. To broadcast across nodes
(chat, live updates), set a `Cluster` on `App` and give the Bus or Value
a topic name - events are then routed through the broker to every node:

```go
app := tether.App{
    Cluster: tetheredis.New(rdb),
}
var messages = tether.NewBus[Message](tether.BusConfig{Topic: "messages"})
```

Groups stay local; cross-node broadcasting flows through clustered Bus
events. See [cluster](cluster.md) for how self-filtering, serialisation,
and custom brokers (NATS, etc. via the two-method `Cluster` interface)
work.

## Capacity planning

`MaxSessions` and `MaxPending` live on `App` (shared across all
handlers). `CmdBufferSize` lives on `StatefulConfig.Limits`
(per-handler).

| Setting | Default | Guidance |
|---------|---------|----------|
| `App.MaxSessions` | 0 (unlimited) | **Set this in production.** Caps total sessions (pending + active + disconnected) |
| `App.MaxPending` | 128 | Caps pre-warmed sessions awaiting transport connection |
| `Limits.CmdBufferSize` | 64 | Increase if `BufferOverflow` diagnostics are frequent |

Use the [health check](operations.md#health-check) endpoint to monitor pool sizes and
feed them into your load balancer's readiness probe.

---

[← Back to documentation](../README.md#documentation)
