# Server-initiated updates

## Pushing state changes

Push state changes from outside the event loop (timers, database changes, broadcasts):

```go
session.Update(func(s State) State {
    s.Message = "New data available"
    return s
})
```

## Page title

```go
session.SetTitle("New Page — My App")
```

## Screen reader announcements

```go
session.Announce("Item added to cart")
```

The JS runtime maintains a hidden `aria-live="polite"` region. Setting its text causes assistive technology to read the announcement aloud.

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

Server-initiated URL changes:

```go
session.Navigate("/success")          // pushState (adds history entry)
session.ReplaceURL("/current?saved=1") // replaceState (no history entry)
```
