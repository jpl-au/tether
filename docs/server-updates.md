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

`Flash` and `Indicator` target DOM elements by CSS selector. This is productive - a single line shows a message or a spinner - but it couples the server to the DOM's ID structure. For simple apps and quick iterations this is the right trade-off.

For reusable components or complex layouts where selectors become fragile, signals achieve the same result without coupling:

```go
// Selector approach - quick and direct
sess.Flash("#notice", "Saved")

// Signal approach - decoupled, no selector needed
sess.Signal("saved", true)
// In Render:
bind.Apply(span.Text("Saved"), bind.BindShow("saved"))
```

```go
// Selector approach - show a spinner by ID
bind.Apply(button.Text("Load"),
    bind.OnClick("load"),
    bind.Indicator("#spinner"),
)

// Signal approach - show a spinner via signal binding
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

## State() and Handle

`State()` returns an atomic snapshot that is updated after each Handle
or Update completes. During Handle, the snapshot is stale - it reflects
the state before Handle was called. This is by design: the command loop
is executing Handle, so no other mutation has happened yet.

**Do not call `State()` inside Handle.** Use the `state` parameter
instead - it is the current, authoritative value:

```go
// Wrong - State() returns the pre-Handle snapshot
Handle: func(sess tether.Session, state State, ev tether.Event) State {
    state.Count++
    live := sess.(*tether.StatefulSession[State])
    count := live.State().Count // BUG: stale, pre-Handle value
    return state
}

// Right - use the state parameter directly
Handle: func(sess tether.Session, state State, ev tether.Event) State {
    state.Count++
    newCount := state.Count // correct, current value
    group.BroadcastOthers(sess, func(t *tether.StatefulSession[State], s State) State {
        s.Count = newCount // capture from parameter, not State()
        return s
    })
    return state
}
```

In dev mode, a warning is emitted when `State()` is called during
Handle to help catch this mistake early.

`State()` is designed for external goroutines - background workers,
timers, and broadcast callbacks that run outside Handle. In those
contexts it always returns the most recently completed state.

## Dynamic keys

The diff engine only tracks elements marked with `.Dynamic("key")`. When state changes and the framework re-renders, it compares the HTML of each keyed element with the previous render. Elements without a Dynamic key are invisible to the diff engine - their changes produce no patches and the client never updates.

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

Both branches of a conditional must produce the same key. If a key appears or disappears between renders, the diff engine treats it as a structural change and falls back to a full root morph - correct but expensive. Keep the key set stable by wrapping conditionals in a keyed container:

```go
// Wrong - key only exists when items are present
if len(items) > 0 {
    return ul.New(nodes...).Dynamic("list")
}
return p.Text("Empty")  // no key - structural change on first item

// Right - key is always present
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

Without this key, navigating between pages changes the rendered HTML but the diff engine produces no patches - the new page never appears.

### OnStructuralChange - observing structural changes

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

### OnNoPatch - observing empty render cycles

When a render cycle produces no patches and no structural change, the framework calls `StatefulConfig.OnNoPatch` if set. This lets you decide how to handle it - log, count, or ignore:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    OnNoPatch: func(sess *tether.StatefulSession[State], info tether.NoPatch) {
        // Signal-only updates (e.g. a ticker) intentionally produce
        // no patches - log at debug. Navigate and event sources that
        // produce nothing are likely missing Dynamic keys - warn.
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

If a handler or Update callback panics, the session is destroyed by default because the state may contain partially mutated maps or slices that cannot be trusted. Any buffered effects (`Toast()`, `Signal()`, `Navigate()`) are discarded. Set `StatefulConfig.OnPanic` to opt into custom recovery - the callback receives the session and the error, and the session is kept alive. In DevMode, a warning explains what was dropped:

```
level=WARN msg="side effects discarded due to handler panic - any Toast, Signal, or Navigate calls before the panic were dropped"
```

These warnings are centralised in the `dev` package - call sites use `dev.Warn()` which silently no-ops outside DevMode.

### Signals bypass the diff engine

Signals (`sess.Signal`, `bind.BindText`, `bind.BindShow`, etc.) update bound elements directly on the client without rendering or diffing. Elements that are updated exclusively via signals do not need Dynamic keys.

## Background goroutines

See [background goroutines](background.md) for `Session.Go`, transport-scoped vs session-scoped contexts, and session methods callable from any goroutine.

## Components

See [components](components.md) for `tether.Component`, declarative mounting, manual routing with `RouteTyped`, and the `Mounter` lifecycle hook.

## URL routing

See [routing](routing.md) for `OnNavigate`, `bind.Link`, and multi-page apps with the `router` package.

---

[← Back to documentation](../README.md#documentation)
