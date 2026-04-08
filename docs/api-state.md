# State management

## Group

Broadcast state changes to multiple sessions:

```go
group := tether.NewGroup[State]()
group.Add(sess)
group.Remove(sess)
group.Len()       // member count (point-in-time)
group.Count()     // *Value[int] - reactive member count
group.All()       // iter.Seq[*StatefulSession[S]]

group.Broadcast(func(target *tether.StatefulSession[State], s State) State {
    s.Message = "hello"
    return s
})

group.BroadcastOthers(sender, func(target *tether.StatefulSession[State], s State) State {
    s.Message = "hello"
    return s
})
```

Optional callbacks: `group.OnJoin`, `group.OnLeave`.
For auto-registration, pass groups via `StatefulConfig.Groups`.

---

## Bus

Typed pub/sub for cross-session communication. Create one per event type at program startup and share it across handlers:

```go
var messages = tether.NewBus[MessageSent]("messages")
```

An optional `BusConfig` can customise async subscriber behaviour:

```go
var events = tether.NewBus[Event]("events", tether.BusConfig{
    AsyncWorkers:  128,           // default 64
    AsyncOverflow: tether.Drop,   // default Block
})
```

| `BusConfig` field | Type | Default | Description |
|-------------------|------|---------|-------------|
| `AsyncWorkers` | `int` | 64 | Maximum concurrent goroutines for async subscribers |
| `AsyncOverflow` | `AsyncOverflow` | `Block` | What happens when all worker slots are full |

`AsyncOverflow` constants:

| Value | Behaviour |
|-------|-----------|
| `Block` | Wait for a slot. No data loss, but the publisher stalls |
| `Drop` | Discard the event and log a warning. Publisher never stalls |
| `Inline` | Run the callback synchronously in the publisher's goroutine |

### Publishing

```go
bus.Publish(msg)         // to all subscribers - use for external sources (DB, queues, cron)
bus.Emit(sess, msg)      // to all except sender - use inside Handle
bus.Len()                // active subscriber count
```

`Emit` accepts any `Session` value, so it can be called directly from `Handle` without a type-assert. In live sessions, publication is enqueued on the sender's command loop so the sender's diff is sent to the client before other subscribers react. Subscriptions registered via `tether.On` whose session ID matches the emitting session are automatically skipped - preventing double-apply since `Handle` already updated the sender's state.

### Subscribing

Raw subscription for non-session consumers (external services, monitoring):

```go
// Synchronous - callback runs in the publisher's goroutine. Must not block.
cancel := bus.Subscribe(ctx, func(msg ChatMessage) { ... })

// Asynchronous - callback runs in its own goroutine per event. Safe for I/O.
cancel := bus.SubscribeAsync(ctx, func(msg ChatMessage) { ... })
```

`Subscribe` runs the callback synchronously in the publisher's goroutine - it must not block. `SubscribeAsync` dispatches each event to a goroutine bounded by a semaphore (default 64 workers, configurable via `BusConfig.AsyncWorkers`). When all slots are full, the `BusConfig.AsyncOverflow` strategy applies: `Block` (wait), `Drop` (discard), or `Inline` (run synchronously). Use `SubscribeAsync` for external consumers that perform database writes, HTTP calls, or other I/O.

Session-aware subscription via `tether.On` - the primary way to connect a Bus to a session:

```go
tether.On(sess, messages, func(msg MessageSent, state ChatState) ChatState {
    state.Messages = append(state.Messages, msg.Text)
    return state
})
```

`tether.On` subscribes the session to the bus. When an event arrives, the callback runs inside the session's command loop (via `Session.Update`) with the event and the current state. The callback returns the new state - same pattern as Update.

Key behaviours:
- **Sender filtering** - if the event was emitted by this session (via `Bus.Emit`), the callback is skipped automatically
- **Auto-cleanup** - the subscription is removed when the session is destroyed (context cancelled)
- **Thread-safe** - the callback runs on the session's command loop, never concurrently with Handle or other Updates

Preferred usage is via `StatefulConfig.Watchers` for declarative subscription:

```go
Watchers: []tether.Watcher[State]{
    tether.WatchBus(activityBus, func(item ActivityItem, s State) State {
        s.Activity = append(s.Activity, item)
        return s
    }),
},
```

`tether.On` is still available for conditional subscriptions in `OnConnect`.

### Bus vs Group

Bus is parameterised on the **event type** - any session can subscribe regardless of its state type. Group requires all sessions to share the same state type. Use Bus for cross-handler communication; use Group for same-handler broadcasting.

---

## Value

Shared observable state that notifies sessions when it changes. Built on top of Bus internally:

```go
var onlineCount = tether.NewValue(0, "online-count")
```

### Reading and writing

```go
v.Load()              // lock-free read - safe from any goroutine
v.Store(val)          // set and notify all observers
v.Update(func(V) V)   // atomic read-modify-write (counters, accumulators)
v.Len()               // active observer count
```

### Observing

`tether.Observe` subscribes a session to a Value. The current value is delivered immediately so the session's state is up to date from the moment of subscription. Future changes via Store or Update are delivered automatically:

```go
tether.Observe(sess, onlineCount, func(count int, s State) State {
    s.OnlineUsers = count
    return s
})
```

Key behaviours:
- **Immediate sync** - the callback fires once with the current value at subscription time
- **Atomic subscribe+read+apply** - the subscription, read, and initial state application happen within a single session command, so a concurrent Store is always ordered after the initial value
- **Auto-cleanup** - removed when the session is destroyed
- **Thread-safe** - runs on the session's command loop

Preferred usage is via `StatefulConfig.Watchers` for declarative subscription:

```go
Watchers: []tether.Watcher[State]{
    tether.WatchValue(onlineCount, func(count int, s State) State {
        s.OnlineCount = count
        return s
    }),
},
```

`tether.Observe` is still available for conditional subscriptions in `OnConnect`.

### Value vs Bus

Use Value for state that multiple sessions need to stay in sync with (online counts, shared configuration, room membership). Use Bus for discrete domain events (chat messages, notifications, activity feeds).

---

## Component

A self-contained rendering unit with its own state. Components know how to render themselves and handle their own events, without any knowledge of the parent's state type:

```go
type Component interface {
    Render() node.Node
    Handle(Session, Event) Component
}
```

Components are value types - `Handle` returns a new value, the receiver is never mutated. This matches the `HandleFunc` pattern (returns new S).

### EqualComponent

Optional interface for fast equality checking. Implement this when your component contains slices or maps that make `reflect.DeepEqual` expensive:

```go
type EqualComponent interface {
    Component
    EqualComponent(Component) bool
}
```

### Route and RouteTyped

Dispatch events to a component by prefix. Events with actions starting with `"prefix."` are routed to the component with the prefix stripped:

```go
// Route returns Component - use when the field stores the interface type.
s.Chat = tether.Route(s.Chat, "chat", sess, ev)

// RouteTyped preserves the concrete type - use when the field stores a concrete struct.
s.Chat = tether.RouteTyped(s.Chat, "chat", sess, ev)
```

`RouteTyped` is the common choice. It preserves compile-time type safety - the parent stores the concrete component type in its state struct with direct field access, no type assertions needed.

### StatefulConfig.Components

Declarative component mounting. The framework intercepts events matching each mount's prefix and dispatches them to the component's `Handle` - the page's `Handle` function never sees these events:

```go
tether.StatefulConfig[State]{
    Components: []tether.ComponentMount[State]{
        tether.Mount("likes",
            func(s State) counter.Counter { return s.Likes },
            func(s State, c counter.Counter) State { s.Likes = c; return s },
        ),
        tether.Mount("stars",
            func(s State) counter.Counter { return s.Stars },
            func(s State, c counter.Counter) State { s.Stars = c; return s },
        ),
    },
}
```

`Mount` follows the same pattern as `WatchValue` and `WatchBus`: a generic constructor that returns a non-generic interface, so `StatefulConfig.Components` can hold mounts for different component types.

Navigate events bypass mounts - they always reach `OnNavigate`.

### Event.Target

When `StatefulConfig.Components` dispatches an event, the framework sets `Event.Target` to the mount's prefix (e.g. `"likes"` or `"stars"`). Middleware and logging can inspect this field to identify which component handled the event without parsing the action string.

### Event.WithAction

Returns a copy of the event with a different `Action`. Used by `Route`, `RouteTyped`, and the mount system to strip prefixes before forwarding to the component.

### Mounter

Optional interface for one-time component setup. The framework calls `Mount` once per component during session startup (after the command loop starts, before any client events arrive) for components registered via `StatefulConfig.Components`:

```go
type Mounter interface {
    Component
    Mount(Session) Component
}
```

Use Mount for initial side effects - `sess.Toast("Ready")`, `sess.Signal(...)`, `sess.Go(...)` - that a component needs when it first appears. Components that don't need setup simply implement `Component` without `Mounter`.

### Route vs StatefulConfig.Components

Use `StatefulConfig.Components` when the component is self-contained and the page's `Handle` never needs to see its events. Use `Route`/`RouteTyped` in Handle when you need to coordinate component events with other state changes.

### RouteMount

Exported function used by the exec loop and `tethertest` to dispatch component events:

```go
func RouteMount[S any](mounts []ComponentMount[S], sess Session, state S, ev Event) (S, bool)
```

Returns the updated state and `true` if a mount handled the event, or the original state and `false` if no prefix matched.

---

## Versioned

`tether.Versioned[T]` wraps data with an automatic version counter
for use with `node.Memoise`. The version increments on every `With`
call, ensuring the memoisation key tracks data changes without manual
bookkeeping.

```go
type State struct {
    Items tether.Versioned[[]Item]
}

// Read:
renderTable(s.Items.Val)

// Update (version increments automatically):
s.Items = s.Items.With(append(s.Items.Val, newItem))

// Memoisation key:
node.Memoise(s.Items.Version(), func() node.Node { ... })
```

| Method | Description |
|--------|-------------|
| `NewVersioned(data)` | Create with initial data (version 1) |
| `v.Val` | The wrapped data (read directly) |
| `v.With(data)` | Return new Versioned with updated data and incremented version |
| `v.Version()` | Current version counter (use as memoisation key) |

Versioned is a value type - it works naturally with tether's state
model. The zero value is valid (version 0).

---

[← API reference](api.md)
