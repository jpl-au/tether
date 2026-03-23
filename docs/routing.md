# URL routing

Bidirectional sync between Go state and the browser URL:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    OnNavigate: func(_ tether.Session, s State, p tether.Params) State {
        s.Page = p.Path
        return s
    },
    // ...
})

// Mark an anchor for client-side navigation
bind.Apply(a.Link("/profile", "Profile"), bind.Link())
```

## Multi-page apps

For multi-page apps, use the `router` package:

```go
r := router.New[State](func(s State) string { return s.Page })
r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
r.Route("/settings", router.Page[State]{Render: settingsRender})
r.NotFound(router.Page[State]{Render: notFoundRender})

tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
    // ...
})
```

---

[← Back to documentation](../README.md#documentation)
