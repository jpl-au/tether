# Broadcasting

## Groups

Push updates to multiple sessions at once:

```go
group := tether.NewGroup[State]()

tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    OnConnect:    func(s *tether.StatefulSession[State]) { group.Add(s) },
    OnDisconnect: func(s *tether.StatefulSession[State]) { group.Remove(s) },
    // ...
})

// Send a message to every connected client
group.Broadcast(func(target *tether.StatefulSession[State], s State) State {
    s.Notification = "System update complete"
    target.Announce("System update complete")
    return s
})
```

Each session is updated concurrently so a slow render in one session does not block delivery to the rest.

For convenience, use `StatefulConfig.Groups` to auto-register sessions without `OnConnect`/`OnDisconnect` boilerplate:

```go
group := tether.NewGroup[State]()

tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Groups: []*tether.Group[State]{group},
    // ...
})
```

Sessions are automatically added when the transport connects and removed when the session is permanently destroyed.

## Broadcasting from Handle

When broadcasting from inside `Handle`, use `BroadcastOthers` to exclude the sender. Handle already updates the sender's state via the return value - broadcasting to everyone would double-apply the change on the sender:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    if ev.Action == "send-message" {
        s.Messages = append(s.Messages, ev.Data["text"])
        // BroadcastOthers accepts Session directly - no type-assert needed.
        group.BroadcastOthers(sess, func(target *tether.StatefulSession[State], s State) State {
            s.Messages = append(s.Messages, ev.Data["text"])
            return s
        })
    }
    return s
},
```

## Presence (Group callbacks)

Track who is online with callbacks and iteration:

```go
group := tether.NewGroup[State]()
group.OnJoin = func(s *tether.StatefulSession[State]) {
    log.Printf("user joined: %s", s.ID())
}
group.OnLeave = func(s *tether.StatefulSession[State]) {
    log.Printf("user left: %s", s.ID())
}

// Iterate over all connected sessions
for s := range group.All() {
    log.Printf("online: %s", s.ID())
}

// Count online sessions
log.Printf("online: %d", group.Len())
```

`OnJoin` fires when `Add` is called with a new session. `OnLeave` fires when `Remove` is called for a session that was in the group. Duplicate adds and absent removes are no-ops.

## Presence tracking (tether.Presence)

`tether.Presence[T]` tracks per-session metadata and makes it available
to all sessions. Use it for collaborative features: who is viewing a
card, who is typing, which page each user is on.

```go
type ViewInfo struct {
    Card string
    Name string
}

var viewers = tether.NewPresence[ViewInfo]()

// In Handle - set when viewing a card:
viewers.Set(sess.ID(), ViewInfo{Card: id, Name: s.Name})

// In Handle - clear when leaving:
viewers.Clear(sess.ID())

// In OnDisconnect - clean up:
viewers.Clear(sess.ID())

// In Render - show what others are doing:
viewers.Each(s.SessionID, func(sid string, v ViewInfo) {
    // v.Name is viewing v.Card - skip the current session
})
```

Methods: `Set`, `Clear`, `Get`, `All`, `Each`, `Len`. The `Each`
method accepts an exclude parameter so renders skip the current
session's own entry.

## Bus - typed cross-session events

`tether.Bus` routes typed domain events to subscribers. Unlike Group, Bus is parameterised on the **event type** rather than the state type, so sessions from different handlers can communicate:

```go
var messages = tether.NewBus[MessageSent]()
```

Subscribe a session declaratively via `StatefulConfig.Watchers`:

```go
Watchers: []tether.Watcher[State]{
    tether.WatchBus(messages, func(msg MessageSent, s State) State {
        s.Messages = append(s.Messages, msg.Text)
        return s
    }),
},
```

Or in `OnConnect` for conditional subscriptions:

```go
OnConnect: func(sess *tether.StatefulSession[State]) {
    tether.On(sess, messages, func(msg MessageSent, s State) State {
        s.Messages = append(s.Messages, msg.Text)
        return s
    })
},
```

Publish from Handle with sender filtering:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    if ev.Action == "send" {
        msg := MessageSent{Text: ev.Value()}
        s.Messages = append(s.Messages, msg.Text)
        // Emit skips the sender - Handle already updated their state directly.
        // Bus.Emit accepts Session, so no type-assert is needed.
        messages.Emit(sess, msg)
    }
    return s
},
```

Publish from external sources (database listeners, message queues) with no sender filter:

```go
messages.Publish(MessageSent{Text: "System announcement"})
```

### Raw subscriptions

For non-session consumers (monitoring, logging, external services), `Subscribe` and `SubscribeAsync` register callbacks directly:

```go
// Synchronous - callback runs in the publisher's goroutine.
// Must not block.
bus.Subscribe(ctx, func(msg MessageSent) {
    metrics.Counter("messages").Inc()
})

// Asynchronous - callback runs in its own goroutine, bounded
// by a semaphore (default 64 workers). Safe for I/O.
bus.SubscribeAsync(ctx, func(msg MessageSent) {
    db.InsertAuditLog(ctx, msg)
})
```

Both variants are cleaned up automatically when `ctx` is cancelled.

Register raw subscriptions via a `Setup(ctx context.Context)` function called from `main` with the root context. This gives subscriptions a managed lifetime tied to the application shutdown signal:

```go
// handler/messages.go
var messageBus = tether.NewBus[MessageSent]()

func Setup(ctx context.Context) {
    messageBus.Subscribe(ctx, func(msg MessageSent) {
        metrics.Counter("messages").Inc()
    })
    messageBus.SubscribeAsync(ctx, func(msg MessageSent) {
        slog.Info("message received", "text", msg.Text)
    })
}

// main.go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
handler.Setup(ctx)
```

Avoid registering raw subscriptions in `init()` - that creates goroutines with no context cancellation, which run until the process exits regardless of graceful shutdown signals.

Session-bound subscriptions (registered via `tether.On`) are cleaned up automatically when the session is destroyed. No manual unsubscribe needed.

### Ordering note for synchronous subscribers

Synchronous `Subscribe` callbacks run inline in the publisher's goroutine. If a callback updates a `Value`, the Value's observers fire immediately - before the original event reaches session command loops via `On`. This can cause a brief inconsistency where a session sees the Value change (e.g. a counter increment) before the event that caused it (e.g. a new message).

The session's FIFO command loop restores consistency within one tick, and in practice the client coalesces rapid renders. But if strict ordering matters, use `SubscribeAsync` instead - it runs in its own goroutine, so the original event reaches session command loops first:

```go
// Ordering-safe: async subscriber doesn't block the publish loop
messageBus.SubscribeAsync(ctx, func(msg MessageSent) {
    messageCount.Update(func(n int) int { return n + 1 })
})
```

## Value - shared observable state

`tether.Value` holds a single value that sessions can observe. When the value changes, all observers are notified automatically. Built on Bus internally:

```go
var onlineCount = tether.NewValue(0)
```

Observe declaratively via `StatefulConfig.Watchers` - the current value is delivered immediately on connect:

```go
Watchers: []tether.Watcher[State]{
    tether.WatchValue(onlineCount, func(count int, s State) State {
        s.OnlineCount = count
        return s
    }),
},
```

Or in `OnConnect` for conditional subscriptions:

```go
OnConnect: func(sess *tether.StatefulSession[State]) {
    tether.Observe(sess, onlineCount, func(count int, s State) State {
        s.OnlineCount = count
        return s
    })
},
```

Update from anywhere:

```go
onlineCount.Update(func(n int) int { return n + 1 })  // atomic increment
onlineCount.Store(42)                                   // replace
current := onlineCount.Load()                           // lock-free read
```

Use Value for state that multiple sessions need to stay in sync with (online counts, shared configuration). Use Bus for discrete domain events (chat messages, activity feeds).

## When to use what

See [choosing the right primitive](reactivity.md#choosing-the-right-primitive) for a comparison of Group, Bus, Value, Signals, and client directives.

---

[← Back to documentation](../README.md#documentation)
