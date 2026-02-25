# Server updates

## Side effects from Handle

`Handle` returns a `HandleResult` that can carry side effects alongside the new state. Side effects are merged into the same update message as the diff, so the client receives everything atomically:

```go
Handle: func(s State, ev poly.Event) poly.HandleResult[State] {
    if ev.Action == "add-todo" {
        s.Todos = append(s.Todos, todo)
        return poly.Result(s).
            WithAnnounce("Todo added").
            WithFlash("#notice", "Item saved")
    }
    return poly.Result(s)
}
```

Available side effects:

| Method | Effect |
|--------|--------|
| `.WithAnnounce(text)` | Screen reader announcement via aria-live region |
| `.WithFlash(selector, text)` | Flash notification at CSS selector, cleared after 5s |
| `.WithTitle(title)` | Set `document.title` |
| `.WithNavigate(url)` | Push URL change with history entry |
| `.WithReplaceURL(url)` | Update URL without history entry |

When no side effects are needed, `poly.Result(s)` returns a plain state-only result.

## Pushing state changes

Push state changes from outside the event loop (timers, database changes, broadcasts):

```go
session.Update(func(s State) State {
    s.Message = "New data available"
    return s
})
```

## Session methods

These methods are safe to call from any goroutine — use them from `OnConnect`, timers, broadcast callbacks, or background workers:

```go
session.SetTitle("New Page — My App")
session.Announce("Item added to cart")
session.Flash("#notice", "Settings saved")
session.Navigate("/success")           // pushState (adds history entry)
session.ReplaceURL("/current?saved=1") // replaceState (no history entry)
```

Each sends a standalone update message. For side effects in response to events, prefer `HandleResult` methods (above) — they merge into the same message as the state diff.

## URL routing

Bidirectional sync between Go state and the browser URL:

```go
poly.New(poly.Config[State]{
    HandleParams: func(state State, params poly.Params) State {
        state.Page = params.Path
        return state
    },
    // ...
})

// Mark an anchor for client-side navigation
poly.Link(a.Link("/profile", "Profile"))
```
