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
Handle: func(sess *poly.Session[State], s State, ev poly.Event) State {
    if ev.Action == "send-message" {
        s.Messages = append(s.Messages, ev.Data["text"])
        group.BroadcastOthers(sess, func(target *poly.Session[State], s State) State {
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
