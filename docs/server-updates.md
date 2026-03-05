# Server updates

## Side effects from Handle

Side effects are called directly on the session parameter during `Handle`. They are buffered and merged into the same update message as the state diff, so the client receives everything atomically:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
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

## Dynamic keys

The diff engine only tracks elements marked with `.Dynamic("key")`. When state changes and the framework re-renders, it compares the HTML of each keyed element with the previous render. Elements without a Dynamic key are invisible to the diff engine — their changes produce no patches and the client never updates.

**This is the most common source of "my state changed but the UI didn't update" bugs.**

### When you need Dynamic keys

Any element whose rendered HTML depends on state needs a Dynamic key, either directly or via a parent container. The rule: if the output changes when state changes, it must be tracked.

```go
// This list changes when state.Files changes, so it needs a key
func fileList(files []FileEntry) node.Node {
    if len(files) == 0 {
        return div.New(
            p.Text("No files yet."),
        ).Dynamic("files")
    }
    return div.New(
        ul.New(renderItems(files)...),
    ).Dynamic("files")
}
```

### Stable key sets

Both branches of a conditional must produce the same key. If a key appears or disappears between renders, the diff engine treats it as a structural change and falls back to a full root morph — correct but expensive. Keep the key set stable by wrapping conditionals in a keyed container:

```go
// Wrong — key only exists when items are present
if len(items) > 0 {
    return ul.New(nodes...).Dynamic("list")
}
return p.Text("Empty")  // no key — structural change on first item

// Right — key is always present
if len(items) == 0 {
    return div.New(p.Text("Empty")).Dynamic("list")
}
return div.New(ul.New(nodes...)).Dynamic("list")
```

### Page content and navigation

In multi-page apps using the `router` package, the page content area should have a Dynamic key so page changes are detected on navigation:

```go
Render: func(s State) node.Node {
    return div.New(
        sidebar(s.Page),
        div.New(r.Render(s)).Dynamic("content"),
    )
},
```

Without this key, navigating between pages changes the rendered HTML but the diff engine produces no patches — the new page never appears.

### OnStructuralChange — observing structural changes

When the diff engine detects a structural change, it falls back to a full root morph. The `Config.OnStructuralChange` callback lets you observe these occurrences for telemetry, metrics, or debugging:

```go
tether.New(tether.Config[State]{
    OnStructuralChange: func(sess *tether.LiveSession[State], change tether.StructuralChange) {
        slog.Warn("structural change",
            "session", sess.ID(),
            "added", change.Added,
            "removed", change.Removed,
            "reordered", change.Reordered,
            "bytes", change.Bytes,
        )
    },
})
```

`StructuralChange.Added` and `Removed` list the Dynamic keys that appeared or disappeared. `Reordered` is true when the same keys exist in both renders but in a different order. `Bytes` is the size of the full HTML sent as a root morph.

When `OnStructuralChange` is nil and DevMode is active, the framework logs a debug message for each occurrence. When DevMode is off and no callback is set, nothing happens.

### OnNoPatch — observing empty render cycles

When a render cycle produces no patches and no structural change, the framework calls `Config.OnNoPatch` if set. This lets you decide how to handle it — log, count, or ignore:

```go
tether.New(tether.Config[State]{
    OnNoPatch: func(sess *tether.LiveSession[State], info tether.NoPatch) {
        // Signal-only updates (e.g. a ticker) intentionally produce
        // no patches — log at debug. Navigate and event sources that
        // produce nothing are likely missing Dynamic keys — warn.
        if info.Source == "update" {
            slog.Debug("no-patch update", "session", sess.ID())
            return
        }
        slog.Warn("no patches produced",
            "session", sess.ID(),
            "source", info.Source,
            "action", info.Action,
        )
    },
})
```

`NoPatch.Source` is `"update"` (from `Session.Update`), `"navigate"` (from a navigation event), or `"event"` (from a click/submit/etc). `NoPatch.Action` carries the event action for event and navigate sources.

When `OnNoPatch` is nil and DevMode is active, the framework logs a debug message for each occurrence. When DevMode is off and no callback is set, nothing happens.

### DevMode warnings

If a handler or Update callback panics after calling `Toast()`, `Signal()`, or `Navigate()`, those buffered effects are discarded. In DevMode, a warning explains what was dropped:

```
level=WARN msg="side effects discarded due to handler panic — any Toast, Signal, or Navigate calls before the panic were dropped"
```

These warnings are centralised in the `dev` package — call sites use `dev.Warn()` which silently no-ops outside DevMode.

### Signals bypass the diff engine

Signals (`sess.Signal`, `bind.BindText`, `bind.BindShow`, etc.) update bound elements directly on the client without rendering or diffing. Elements that are updated exclusively via signals do not need Dynamic keys.

## Background goroutines

Use `Session.Go` to launch background work tied to a session's lifetime. The context is cancelled when the session is permanently destroyed (reaped or shutdown), but survives temporary disconnects:

```go
OnConnect: func(s *tether.LiveSession[State]) {
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

## Scope — component state isolation

`tether.Scope` focuses a session's state onto a smaller component type. The component handler only sees its own sub-state — never the full application state:

```go
var todos = tether.Scope[AppState, TodoState]{
    View:   func(s AppState) TodoState { return s.Todos },
    Update: func(s AppState, t TodoState) AppState { s.Todos = t; return s },
}
```

Use `Handle` in the event handler to dispatch to the component:

```go
Handle: func(sess tether.Session, s AppState, ev tether.Event) AppState {
    if ev.Action == "add-todo" || ev.Action == "remove-todo" {
        return todos.Handle(sess, s, ev, todoHandle)
    }
    return s
},
```

Use `With` inside `Session.Update` for server-initiated changes:

```go
sess.Update(func(s AppState) AppState {
    return todos.With(s, func(t TodoState) TodoState {
        t.Valid = true
        return t
    })
})
```

Scope keeps component handlers reusable — they work with `TodoState` and never import the parent `AppState` type.

## URL routing

Bidirectional sync between Go state and the browser URL:

```go
tether.New(tether.Config[State]{
    OnNavigate: func(_ tether.Session, s State, p tether.Params) State {
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

tether.New(tether.Config[State]{
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
    // ...
})
```
