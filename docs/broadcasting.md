# Broadcasting

## Groups

Push updates to multiple sessions at once:

```go
group := poly.NewGroup[State]()

poly.New(poly.Config[State]{
    OnConnect:    func(s *poly.Session[State]) { group.Add(s) },
    OnDisconnect: func(s *poly.Session[State]) { group.Remove(s) },
    // ...
})

// Send a message to every connected client
group.Broadcast(func(target *poly.Session[State], s State) State {
    s.Notification = "System update complete"
    target.Announce("System update complete")
    return s
})
```

Each session is updated concurrently so a slow render in one session does not block delivery to the rest.

For convenience, use `Config.Groups` to auto-register sessions without `OnConnect`/`OnDisconnect` boilerplate:

```go
group := poly.NewGroup[State]()

poly.New(poly.Config[State]{
    Groups: []*poly.Group[State]{group},
    // ...
})
```

Sessions are automatically added when the transport connects and removed when the session is permanently destroyed.

## Broadcasting from Handle

When broadcasting from inside `Handle`, use `BroadcastOthers` to exclude the sender. Handle already updates the sender's state via the return value — broadcasting to everyone would double-apply the change on the sender:

```go
Handle: func(sess poly.PreSession, s State, ev poly.Event) State {
    if ev.Action == "send-message" {
        s.Messages = append(s.Messages, ev.Data["text"])
        // In live mode, sess is a *Session — type-assert to access
        // Broadcast, Update, and other session-specific methods.
        live := sess.(*poly.Session[State])
        group.BroadcastOthers(live, func(target *poly.Session[State], s State) State {
            s.Messages = append(s.Messages, ev.Data["text"])
            return s
        })
    }
    return s
},
```

## Presence

Track who is online with callbacks and iteration:

```go
group := poly.NewGroup[State]()
group.OnJoin = func(s *poly.Session[State]) {
    log.Printf("user joined: %s", s.ID())
}
group.OnLeave = func(s *poly.Session[State]) {
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

## Bus — typed cross-session events

`poly.Bus` routes typed domain events to subscribers. Unlike Group, Bus is parameterised on the **event type** rather than the state type, so sessions from different handlers can communicate:

```go
var messages = poly.NewBus[MessageSent]()
```

Subscribe a session in `OnConnect`:

```go
OnConnect: func(sess *poly.Session[State]) {
    poly.On(messages, sess, func(msg MessageSent, s State) State {
        s.Messages = append(s.Messages, msg.Text)
        return s
    })
},
```

Publish from Handle with sender filtering:

```go
Handle: func(sess poly.PreSession, s State, ev poly.Event) State {
    if ev.Action == "send" {
        msg := MessageSent{Text: ev.Value()}
        s.Messages = append(s.Messages, msg.Text)
        // Emit skips the sender — Handle already updated their state
        messages.Emit(sess.(*poly.Session[State]), msg)
    }
    return s
},
```

Publish from external sources (database listeners, message queues) with no sender filter:

```go
messages.Publish(MessageSent{Text: "System announcement"})
```

Subscriptions are cleaned up automatically when the session is destroyed. No manual unsubscribe needed.

## Value — shared observable state

`poly.Value` holds a single value that sessions can observe. When the value changes, all observers are notified automatically. Built on Bus internally:

```go
var onlineCount = poly.NewValue(0)
```

Observe in `OnConnect` — the current value is delivered immediately:

```go
OnConnect: func(sess *poly.Session[State]) {
    poly.Observe(onlineCount, sess, func(count int, s State) State {
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

| Primitive | Parameterised on | Best for |
|-----------|-----------------|----------|
| **Group** | State type | Broadcasting state mutations to sessions of the same handler |
| **Bus** | Event type | Discrete events across handlers (chat messages, activity feeds) |
| **Value** | Value type | Shared state all sessions should track (online count, config) |
