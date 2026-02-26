# Server updates

## Side effects from Handle

Side effects are called directly on the session parameter during `Handle`. They are buffered and merged into the same update message as the state diff, so the client receives everything atomically:

```go
Handle: func(sess *poly.Session[State], s State, ev poly.Event) State {
    if ev.Action == "add-todo" {
        s.Todos = append(s.Todos, todo)
        sess.Announce("Todo added")
        sess.Flash("#notice", "Item saved")
    }
    return s
},
```

Available side effects:

| Method | Effect |
|--------|--------|
| `sess.Toast(text)` | Global notification (the client shows and auto-dismisses it) |
| `sess.Announce(text)` | Screen reader announcement via aria-live region |
| `sess.Flash(selector, text)` | Notification at CSS selector, cleared after 5s |
| `sess.SetTitle(title)` | Set `document.title` |
| `sess.Navigate(url)` | Push URL change with history entry |
| `sess.ReplaceURL(url)` | Update URL without history entry |
| `sess.Signal(key, value)` | Push a reactive value to bound elements |
| `sess.Signals(map)` | Push multiple reactive values at once |

When no side effects are needed, just return the new state.

## Pushing state changes

Push state changes from outside the event loop (timers, database changes, broadcasts):

```go
session.Update(func(s State) State {
    s.Message = "New data available"
    return s
})
```

## Background goroutines

Use `Session.Go` to launch background work tied to a session's lifetime. The context is cancelled when the session is permanently destroyed (reaped or shutdown), but survives temporary disconnects:

```go
OnConnect: func(s *poly.Session[State]) {
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

`Session.Context()` returns the context directly for passing to database queries, HTTP clients, or other context-aware APIs.

## Session methods

These methods are safe to call from any goroutine — use them from `OnConnect`, timers, broadcast callbacks, or background workers:

```go
session.Toast("Settings saved")
session.SetTitle("New Page — My App")
session.Announce("Item added to cart")
session.Flash("#notice", "Settings saved")
session.Navigate("/success")           // pushState (adds history entry)
session.ReplaceURL("/current?saved=1") // replaceState (no history entry)
session.Signal("count", 42)            // push a reactive value
```

Each sends a standalone update message. For side effects during event handling, call them on the session parameter inside `Handle` — they merge into the same message as the state diff.

## URL routing

Bidirectional sync between Go state and the browser URL:

```go
poly.New(poly.Config[State]{
    OnNavigate: func(_ poly.PreSession, s State, p poly.Params) State {
        s.Page = p.Path
        return s
    },
    // ...
})

// Mark an anchor for client-side navigation
bind.Link(a.Link("/profile", "Profile"))
```

For multi-page apps, use the `router` package:

```go
r := router.New[State](func(s State) string { return s.Page })
r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
r.Route("/settings", router.Page[State]{Render: settingsRender})
r.NotFound(router.Page[State]{Render: notFoundRender})

poly.New(poly.Config[State]{
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, path string) { s.Page = path }),
    // ...
})
```
