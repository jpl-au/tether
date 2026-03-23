# Background goroutines

Use `Session.Go` to launch background work tied to the transport's lifetime. The context is cancelled when the client disconnects or the session freezes. On reconnect, `OnConnect`/`OnRestore` fires again and can spawn fresh goroutines - no duplicates accumulate:

```go
OnConnect: func(s *tether.StatefulSession[State]) {
    s.Go(func(ctx context.Context) {
        ticker := time.NewTicker(time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                s.Update(func(st State) State {
                    st.Uptime++
                    return st
                })
            }
        }
    })
},
```

No global maps, no done channels, no OnDisconnect cleanup needed.

## Session-lifetime goroutines

For the rare case where a goroutine must survive disconnects (e.g. a long-running database migration), use `Session.Context()` directly:

```go
go func(ctx context.Context) {
    // This goroutine lives until the session is permanently destroyed.
    result := expensiveMigration(ctx)
    sess.Update(func(s State) State { s.Result = result; return s })
}(sess.Context())
```

`Session.Context()` returns the session-lifetime context - cancelled only on permanent destruction (reaper, shutdown, or explicit close).

## Session methods

These methods are safe to call from any goroutine - use them from `OnConnect`, timers, broadcast callbacks, or background workers:

```go
session.Toast("Settings saved")
session.SetTitle("New Page - My App")
session.Announce("Item added to cart")
session.Flash("#notice", "Settings saved")
session.Navigate("/success")           // pushState (adds history entry)
session.ReplaceURL("/current?saved=1") // replaceState (no history entry)
session.Signal("count", 42)            // push a reactive value
```

Each sends a standalone update message. For side effects during event handling, call them on the session parameter inside `Handle` - they merge into the same message as the state diff.

---

[← Back to documentation](../README.md#documentation)
