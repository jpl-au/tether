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

### Selector helpers vs signals

`Flash` and `Indicator` target DOM elements by CSS selector. This is productive — a single line shows a message or a spinner — but it couples the server to the DOM's ID structure. For simple apps and quick iterations this is the right trade-off.

For reusable components or complex layouts where selectors become fragile, signals achieve the same result without coupling:

```go
// Selector approach — quick and direct
sess.Flash("#notice", "Saved")

// Signal approach — decoupled, no selector needed
sess.Signal("saved", true)
// In Render:
bind.Apply(span.Text("Saved"), bind.BindShow("saved"))
```

```go
// Selector approach — show a spinner by ID
bind.Apply(button.Text("Load"),
    bind.OnClick("load"),
    bind.Indicator("#spinner"),
)

// Signal approach — show a spinner via signal binding
bind.Apply(button.Text("Load"),
    bind.OnClick("load"),
    bind.Optimistic("loading", "true"),
)
bind.Apply(span.Text("Loading..."), bind.BindShow("loading"))
```

Both are valid. Use selector helpers when speed matters and the DOM structure is straightforward. Use signals when the element lives in a different component or when the same indicator pattern is reused across pages.

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

When the diff engine detects a structural change, it falls back to a full root morph. The `StatefulConfig.OnStructuralChange` callback lets you observe these occurrences for telemetry, metrics, or debugging:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    OnStructuralChange: func(sess *tether.StatefulSession[State], change tether.StructuralChange) {
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

When a render cycle produces no patches and no structural change, the framework calls `StatefulConfig.OnNoPatch` if set. This lets you decide how to handle it — log, count, or ignore:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    OnNoPatch: func(sess *tether.StatefulSession[State], info tether.NoPatch) {
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

## Components — reusable state isolation

`tether.Component` is a self-contained rendering unit with its own state. Components know how to render themselves and handle their own events, without any knowledge of the parent's state type:

```go
type Counter struct {
    Count int
}

func (c Counter) Render() node.Node {
    return div.New(
        span.Textf("Count: %d", c.Count).Dynamic("count"),
        bind.Apply(button.Text("+1"), bind.OnClick("increment")),
        bind.Apply(button.Text("Reset"), bind.OnClick("reset")),
    )
}

func (c Counter) Handle(sess tether.Session, ev tether.Event) tether.Component {
    switch ev.Action {
    case "increment":
        c.Count++
    case "reset":
        c.Count = 0
        sess.Toast("Counter reset")
    }
    return c
}
```

Components are value types — `Handle` returns a new value, the receiver is never mutated. Side effects (`sess.Toast`, `sess.Signal`, etc.) work inside components just like they do in the page handler.

### Declarative mounting with StatefulConfig.Components

For components that are fully self-contained, mount them declaratively on StatefulConfig. The framework intercepts events matching the mount's prefix and dispatches them automatically — the page's `Handle` never sees these events:

```go
tether.StatefulConfig[State]{
    Components: []tether.ComponentMount[State]{
        tether.Mount("likes",
            func(s State) Counter { return s.Likes },
            func(s State, c Counter) State { s.Likes = c; return s },
        ),
    },
}
```

In Render, call the component's `Render` method:

```go
Render: func(s State) node.Node {
    return div.New(
        p.Text("Likes:"),
        div.New(s.Likes.Render()).Dynamic("likes-section"),
    )
},
```

### Manual routing with RouteTyped

When you need to coordinate component events with other state changes, or when using `tether.Stateless` (which does not support `StatefulConfig.Components`), route events manually in Handle:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    s.Counter = tether.RouteTyped(s.Counter, "counter", sess, ev)
    return s
},
```

Events with actions like `"counter.increment"` are forwarded to the component with the prefix stripped — the component sees `"increment"`. Events without a matching prefix pass through unchanged.

### Initial setup with Mounter

Components that need one-time setup (firing a toast, pushing a signal, starting background work) can implement the optional `Mounter` interface:

```go
func (d Dashboard) Mount(sess tether.Session) tether.Component {
    sess.Toast("Dashboard ready")
    return d
}
```

The framework calls `Mount` once per component during session startup for components registered via `StatefulConfig.Components`. Components that don't need setup simply omit the method.

## URL routing

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
