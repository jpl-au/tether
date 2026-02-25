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
group.Broadcast(func(s State) State {
    s.Notification = "System update complete"
    return s
})
```

Sessions are updated concurrently so a slow render in one session does not block delivery to the rest. `Broadcast` is fire-and-forget — it returns immediately after spawning the update goroutines. Each goroutine completes after a single render-diff-send cycle.

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
