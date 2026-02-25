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
group.Broadcast(func(s State) poly.HandleResult[State] {
    s.Notification = "System update complete"
    return poly.Result(s).WithAnnounce("System update complete")
})
```

Sessions are updated concurrently so a slow render in one session does not block delivery to the rest. `Broadcast` is fire-and-forget — it returns immediately after spawning the update goroutines. Each goroutine completes after a single render-diff-send cycle.

## Broadcasting from Handle

When broadcasting from inside `Handle`, use `BroadcastOthers` to exclude the sender. Handle already updates the sender's state via the return value — broadcasting to everyone would double-apply the change on the sender:

```go
Handle: func(sess *poly.Session[State], s State, ev poly.Event) poly.HandleResult[State] {
    if ev.Action == "send-message" {
        s.Messages = append(s.Messages, ev.Data["text"])
        group.BroadcastOthers(sess, func(s State) poly.HandleResult[State] {
            s.Messages = append(s.Messages, ev.Data["text"])
            return poly.Result(s)
        })
    }
    return poly.Result(s)
},
```

## Presence

Track who is online with callbacks and member listing:

```go
group := poly.NewGroup[State]()
group.OnJoin = func(s *poly.Session[State]) {
    log.Printf("user joined: %s", s.ID())
}
group.OnLeave = func(s *poly.Session[State]) {
    log.Printf("user left: %s", s.ID())
}

// Get all connected sessions
members := group.Members()
```

`OnJoin` fires when `Add` is called with a new session. `OnLeave` fires when `Remove` is called for a session that was in the group. Duplicate adds and absent removes are no-ops.
