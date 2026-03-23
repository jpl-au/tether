# Reactivity

Tether is event-driven at every layer. Client interactions, server-side state mutations, cross-session communication, and UI updates all flow through the same reactive model. This guide explains the design, how the primitives compose, and when to reach for each one.

## The command loop  - why events, not mutexes

Every session has a single goroutine that processes all state changes:

```
client events ──► ┌─────────────┐
                  │             │
commands ────────►│ command loop│──► render ──► diff ──► send
(Update, On,      │             │
 Observe, etc.)   └─────────────┘
                        ▲
effects ────────────────┘
(Go goroutines, timers)
```

All mutations are serialised through this loop. `Session.Update` enqueues a closure; `tether.On` and `tether.Observe` deliver external events as closures on the same channel. No mutex, no data race, no deadlock. This is the foundation that makes the rest of the reactive model safe.

See [architecture](architecture.md#the-command-loop) for the full loop implementation.

## Data flow

Reactivity in tether flows in three directions. Each direction has its own set of primitives:

```
                    ┌──────────────────────────┐
                    │       Server state        │
                    │     (session command loop) │
                    └──────┬──────────┬─────────┘
            events ▲       │          │
          (OnClick,│       │ render   │ signals
           OnSubmit│       │ + diff   │ (Signal, BindText,
           etc.)   │       ▼          ▼  BindShow, etc.)
                    ┌──────────────────────────┐
                    │         Browser DOM        │
                    └───────────────────────────┘

                    ┌─────────┐  Bus/Value/Group  ┌─────────┐
                    │Session A│◄─────────────────►│Session B│
                    └─────────┘                    └─────────┘
```

**Client → server:** DOM events bound with `bind.OnClick`, `bind.OnSubmit`, etc. arrive as `tether.Event` values and are dispatched through `Handle`. See [events](events.md).

**Server → client (rendering):** `Handle` returns new state, the framework renders, diffs, and sends patches or morphs. This is the default update path. See [server updates](server-updates.md).

**Server → client (signals):** `sess.Signal` pushes individual values to bound elements without a render cycle. Ideal for high-frequency updates. See [signals](signals.md).

**Server → server:** Bus, Value, and Group let sessions react to each other. This is tether's observer layer, and the focus of the rest of this document.

## The observer primitives

Tether provides three primitives for cross-session reactivity. They share a common design: lock-free reads, copy-on-write for writes, and automatic cleanup when sessions are destroyed.

### Bus  - typed publish/subscribe

`Bus[T]` is the general-purpose pub/sub mechanism. It routes typed domain events to subscribers. Any session from any handler can subscribe, because Bus is parameterised on the **event type**, not the state type:

```go
var messages = tether.NewBus[MessageSent]()

// Publish from anywhere
messages.Publish(MessageSent{Text: "hello"})

// Publish from Handle (skips the sender)
messages.Emit(sess, MessageSent{Text: "hello"})
```

Bus is the foundation. Value is built on Bus internally. `Handler.Diagnostics` is a `Bus[Diagnostic]`. Whenever you see pub/sub behaviour in tether, Bus is the mechanism underneath.

See [broadcasting](broadcasting.md#bus---typed-cross-session-events) for the full API.

### Value  - shared observable state

`Value[T]` holds a single value that sessions can observe. When the value changes, all observers are notified. Built on Bus internally, it adds two things Bus does not provide:

1. **Current value**  - `Load()` returns the latest value with a lock-free atomic read
2. **Immediate sync**  - when a session subscribes, the callback fires once with the current value so the session starts in sync

```go
var onlineCount = tether.NewValue(0)

// Update from anywhere
onlineCount.Update(func(n int) int { return n + 1 })

// Read without subscribing
current := onlineCount.Load()
```

Use Value for state that multiple sessions need to stay in sync with (online counts, shared configuration, feature flags). Use Bus for discrete events where there is no meaningful "current value" (chat messages, activity feeds).

See [broadcasting](broadcasting.md#value---shared-observable-state) for the full API.

### Group  - same-type broadcast

`Group[S]` broadcasts state mutations to sessions that share the same state type. Unlike Bus and Value, Group gives the callback direct access to each target session's state:

```go
group.Broadcast(func(target *tether.StatefulSession[State], s State) State {
    s.Notification = "System update"
    return s
})
```

Group is the right tool when every session of the same handler needs to apply the same state transformation. For discrete events across different handlers, use Bus.

See [broadcasting](broadcasting.md#groups) for the full API.

## Subscribing sessions

There are two ways to connect a session to Bus or Value. Both deliver events through the session's command loop, so callbacks never run concurrently with Handle.

### Declarative  - StatefulConfig.Watchers

The preferred approach. Declare subscriptions alongside the handler configuration:

```go
tether.StatefulConfig[State]{
    Watchers: []tether.Watcher[State]{
        tether.WatchBus(messages, func(msg MessageSent, s State) State {
            s.Messages = append(s.Messages, msg.Text)
            return s
        }),
        tether.WatchValue(onlineCount, func(count int, s State) State {
            s.OnlineCount = count
            return s
        }),
    },
}
```

Watchers are activated during `OnConnect` and cleaned up automatically when the session is destroyed. No manual subscribe/unsubscribe.

### Imperative  - tether.On and tether.Observe

For conditional subscriptions that depend on runtime state (user role, feature flags, URL path), subscribe in `OnConnect`:

```go
OnConnect: func(sess *tether.StatefulSession[State]) {
    if sess.State().IsAdmin {
        tether.On(sess, adminEvents, func(ev AdminEvent, s State) State {
            s.AdminLog = append(s.AdminLog, ev)
            return s
        })
    }
    tether.Observe(sess, onlineCount, func(count int, s State) State {
        s.OnlineCount = count
        return s
    })
},
```

`tether.On` subscribes to a Bus. `tether.Observe` subscribes to a Value (and immediately delivers the current value). Both are cleaned up when the session's context is cancelled.

### Raw subscriptions  - non-session consumers

For monitoring, metrics, logging, or bridging to external systems, `Subscribe` and `SubscribeAsync` register callbacks directly on a Bus:

```go
messages.Subscribe(ctx, func(msg MessageSent) {
    metrics.Counter("messages").Inc()
})

messages.SubscribeAsync(ctx, func(msg MessageSent) {
    db.InsertAuditLog(ctx, msg)
})
```

These are not session-bound  - they run for the lifetime of the context. Use `Subscribe` for non-blocking work (metrics, counters). Use `SubscribeAsync` for I/O (database, HTTP).

See [broadcasting](broadcasting.md#raw-subscriptions) for lifetime management guidance.

## How the primitives compose

The primitives are layered, not isolated:

```
┌─────────────────────────────────────┐
│            Value[T]                 │  shared observable state
│  (atomic read + immediate sync)     │
├─────────────────────────────────────┤
│             Bus[T]                  │  typed publish/subscribe
│  (the universal event backbone)     │
├────────────┬────────────────────────┤
│ Session    │ Raw                    │
│ subscribers│ subscribers            │
│ (On,       │ (Subscribe,            │
│  Observe,  │  SubscribeAsync)       │
│  Watchers) │                        │
└────────────┴────────────────────────┘
```

- **Value wraps Bus.** Every `Store` or `Update` on a Value publishes the new value through its internal Bus. Observers registered via `tether.Observe` or `WatchValue` are Bus subscribers under the hood.
- **Diagnostics is a Bus.** `Handler.Diagnostics` is a `Bus[Diagnostic]`  - the same pub/sub mechanism used for application events. Subscribe to it with `Subscribe` or `SubscribeAsync` for monitoring, alerting, and operational visibility. See [operations](operations.md#diagnostics-bus).
- **Group is independent.** Group does not use Bus internally  - it iterates members and enqueues updates directly. It exists because broadcasting a state mutation ("apply this function to every session's state") is a different operation from publishing an event ("here is a fact, react however you like").

## Choosing the right primitive

| Question | Answer | Use |
|----------|--------|-----|
| Do all subscribers share the same state type? | Yes | **Group** |
| Is there a meaningful "current value" new subscribers need? | Yes | **Value** |
| Are subscribers from different handlers? | Yes | **Bus** |
| Is the update a discrete event (message, notification)? | Yes | **Bus** |
| Does the server need to push a value to the DOM without rendering? | Yes | **Signal** (`sess.Signal`) |
| Does the client need instant feedback without a server round-trip? | Yes | **Client directive** (`bind.ToggleSignal`, `bind.SetSignal`) |

These are not mutually exclusive. A chat application might use Bus for message events, Value for the online user count, Group for typing indicators, and Signals for unread badge counts  - all in the same handler.

## Lifecycle and cleanup

| Subscription type | Cleanup |
|-------------------|---------|
| `StatefulConfig.Watchers` | Automatic on session destroy |
| `tether.On` / `tether.Observe` | Automatic on session destroy (context cancellation) |
| `Bus.Subscribe` / `Bus.SubscribeAsync` | Automatic on context cancellation |
| `StatefulConfig.Groups` | Automatic add on connect, remove on destroy |
| `Group.Add` / `Group.Remove` | Manual (typically in `OnConnect` / `OnDisconnect`) |

Session-bound subscriptions never leak. Raw subscriptions are tied to the context passed at registration  - use the application's root context (from `signal.NotifyContext`) so they are cleaned up on shutdown.

---

[← Back to documentation](../README.md#documentation)
