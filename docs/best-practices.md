# Best practices

## Use the state parameter, not State(), inside Handle

Inside Handle, use the `state` parameter - it is the current value. `State()` returns a stale snapshot from before Handle was called. See [State() and Handle](server-updates.md#state-and-handle) for details and examples.

When broadcasting from Handle, capture values from the parameter:

```go
state.Count++
newCount := state.Count
group.BroadcastOthers(sess, func(t *tether.StatefulSession[State], s State) State {
    s.Count = newCount // captured from parameter
    return s
})
```

In dev mode, a warning is emitted if `State()` is called during Handle.

## Keep Handle fast

Handle runs inside the session's command loop. While it executes, no other events, commands, or effects are processed for that session. The client sees a frozen page.

```go
// Wrong - blocks the loop for the duration of the query
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    rows, _ := db.Query("SELECT * FROM items WHERE user_id = ?", s.UserID)
    s.Items = scanItems(rows)
    return s
},
```

Move slow work into a background goroutine and push the result back via `Update`:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    if ev.Action == "refresh" {
        live := sess.(*tether.StatefulSession[State])
        live.Go(func(ctx context.Context) {
            rows, _ := db.QueryContext(ctx, "SELECT * FROM items WHERE user_id = ?", s.UserID)
            items := scanItems(rows)
            live.Update(func(s State) State {
                s.Items = items
                return s
            })
        })
    }
    return s
},
```

The same applies to `OnConnect` - blocking there delays the session becoming interactive. Use `Session.Go` for database lookups, API calls, or any I/O that might be slow.

## Always key state-dependent elements

The diff engine only tracks elements marked with `.Dynamic("key")`. If state changes but the affected element has no key, the diff produces no patches and the client never updates. This is silent - no error, no warning.

```go
// Wrong - no Dynamic key, changes are invisible to the diff engine
span.Textf("Count: %d", state.Count)

// Right
span.Textf("Count: %d", state.Count).Dynamic("count")
```

Use `StatefulConfig.OnNoPatch` to detect missing keys in development and production:

```go
OnNoPatch: func(sess *tether.StatefulSession[State], info tether.NoPatch) {
    if info.Source != "update" {
        slog.Warn("no patches produced",
            "session", sess.ID(),
            "source", info.Source,
            "action", info.Action,
        )
    }
},
```

See [Dynamic keys](server-updates.md#dynamic-keys) for the full guide.

## Keep key sets stable

When the set of Dynamic keys changes between renders - keys added, removed, or reordered - the diff engine falls back to a full root morph. Correct but expensive.

```go
// Wrong - the key disappears when items is empty
if len(items) > 0 {
    return ul.New(nodes...).Dynamic("list")
}
return p.Text("Empty")

// Right - the key is always present
if len(items) == 0 {
    return div.New(p.Text("Empty")).Dynamic("list")
}
return div.New(ul.New(nodes...)).Dynamic("list")
```

Use `StatefulConfig.OnStructuralChange` to detect unstable key sets. See [stable key sets](server-updates.md#stable-key-sets).

## Use signals for high-frequency updates

A full render-diff-send cycle for a single counter increment is wasteful. Signals push values directly to bound elements - no render, no diff, no HTML:

```go
// In Render - bind the element once
bind.Apply(span.New(), bind.BindText("count"))

// From anywhere - push the value
sess.Signal("count", 42)
```

Signals are ideal for counters, progress bars, status indicators, and anything where the DOM change is a single text or attribute update. See [signals](signals.md) for the full guide.

## Prefer SetData in hot render loops

`bind.Apply` is convenient but adds ~250ns per element. For most pages this is negligible. For render functions that produce thousands of event-bound elements - large tables, long lists - use `SetData` directly:

```go
// Apply - clearer, slightly slower
bind.Apply(button.Text("+"), bind.OnClick("increment"))

// Direct - faster in bulk
button.Text("+").SetData("tether-click", "increment")
```

See [performance](performance.md) for benchmarks.

## Isolate cross-session state

Each session has its own state. Shared mutable state at the package level creates data races - multiple session goroutines read and write it concurrently without synchronisation.

```go
// Wrong - package-level mutable state accessed from multiple sessions
var onlineUsers int

OnConnect: func(sess *tether.StatefulSession[State]) {
    onlineUsers++ // data race
},
```

Use `Value` for shared state that sessions observe, `Bus` for discrete events, and `Group` for broadcasting state mutations:

```go
var onlineCount = tether.NewValue(0)

OnConnect: func(sess *tether.StatefulSession[State]) {
    onlineCount.Update(func(n int) int { return n + 1 })
    tether.Observe(sess, onlineCount, func(count int, s State) State {
        s.OnlineUsers = count
        return s
    })
},
```

`Value`, `Bus`, and `Group` are all internally synchronised. See [broadcasting](broadcasting.md) for when to use each.

## Declare subscriptions on StatefulConfig

Use `StatefulConfig.Watchers` to subscribe sessions to shared Values and Buses declaratively. Watchers are subscribed before `OnConnect` runs and cleaned up automatically when the session is destroyed:

```go
tether.StatefulConfig[State]{
    Watchers: []tether.Watcher[State]{
        tether.WatchValue(onlineCount, func(n int, s State) State {
            s.OnlineUsers = n
            return s
        }),
        tether.WatchBus(messages, func(msg Message, s State) State {
            s.Messages = append(s.Messages, msg)
            return s
        }),
    },
}
```

This keeps all reactive subscriptions visible in one place - right next to `StatefulConfig.Groups`. Reserve `OnConnect` for imperative setup: incrementing counters, publishing events, pushing initial signals, starting background tickers.

Never subscribe inside Handle - it creates a new subscription on every event:

```go
// Wrong - creates a new subscription on every click event
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    live := sess.(*tether.StatefulSession[State])
    tether.Observe(live, onlineCount, func(count int, s State) State {
        s.OnlineUsers = count
        return s
    })
    return s
},
```

`tether.Observe` and `tether.On` are still available for subscriptions that need to happen conditionally or later in the lifecycle (e.g. inside `OnConnect`).

Subscriptions created in `OnConnect` are cleaned up automatically when the session is destroyed. No manual unsubscribe needed.

## Use StatefulConfig.Components for self-contained components

When a component handles its own events without needing to coordinate with the rest of the page, mount it declaratively via `StatefulConfig.Components`. The framework dispatches events automatically - the page's `Handle` never sees them:

```go
Components: []tether.ComponentMount[State]{
    tether.Mount("likes",
        func(s State) counter.Counter { return s.Likes },
        func(s State, c counter.Counter) State { s.Likes = c; return s },
    ),
},
```

Use `Route`/`RouteTyped` in Handle instead when:
- The component needs to coordinate with other state changes
- You are using `tether.Stateless` (stateless handlers don't support `StatefulConfig.Components`)
- You need to inspect or transform the event before forwarding

## Keep component Render roots keyed

A component's `Render` method should produce a node tree with a stable Dynamic key. Without one, changes to the component produce no patches and the client never updates:

```go
func (c Counter) Render() node.Node {
    return div.New(
        span.Textf("Count: %d", c.Count).Dynamic("count"),
        bind.Apply(button.Text("+1"), bind.OnClick("increment")),
    )
}
```

When mounting multiple instances, wrap each in a keyed container so the diff engine can distinguish them:

```go
div.New(s.Likes.Render()).Dynamic("likes-section"),
div.New(s.Stars.Render()).Dynamic("stars-section"),
```

## Use Equal to skip unchanged renders

When many events leave state unchanged - keypresses that don't affect the model, button clicks that only trigger side effects - the render and diff are wasted work. Provide an `Equal` function to skip them:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Equal: func(a, b State) bool {
        return a == b
    },
    // ...
})
```

When `Equal` returns true, the render, diff, and send are skipped entirely. Side effects (Toast, Signal, etc.) are still sent.

For struct types with slice or map fields, `a == b` does not compile - use `reflect.DeepEqual` or write a field-by-field comparison. A manual comparison is faster and avoids reflecting over fields that don't affect rendering.

## Leave idle timeout disabled unless you need it

The default `Timeouts.Idle` is 0 (disabled). Sessions stay alive
as long as the transport is connected, regardless of user activity.
This is the correct default for most applications - dashboards,
kanban boards, chat rooms, and any page a user might leave open.

Only enable an idle timeout if you genuinely need to reclaim server
resources from abandoned sessions. When enabled, sessions that
receive no client events within the duration are closed, the
browser sees a disconnect, and the user has to reconnect. This is
disruptive and should not be the default experience.

```go
// Don't do this unless you have a specific reason:
Timeouts: tether.Timeouts{Idle: 10 * time.Minute},

// The default (zero) is almost always correct:
// Timeouts: tether.Timeouts{},
```

## Choose the right asset mode

Tether supports two asset modes. Use the one that fits your deployment:

**Embedded assets** (default) - bundle everything into the binary:

```go
//go:embed static
var staticFS embed.FS

var assets = &tether.Asset{
    FS:     staticFS,
    Prefix: "/static/",
}
```

Use this for production single-binary deployments. Hashes are computed
once at startup and never change. Zero filesystem dependencies at
runtime.

**Filesystem assets** - serve from disk with live updates:

```go
var assets = &tether.Asset{
    FS:       os.DirFS("./static"),
    Prefix:   "/static/",
    WatchDir: "./static",
}
```

Use this when assets are managed separately from the binary (CDN
deploys, design team editing CSS independently, or development without
`embed`). The watcher recomputes hashes only when files change. Call
`assets.Close()` on shutdown to stop the watcher goroutine.

Do not use `WatchDir` with `embed.FS` - embedded filesystems are
immutable and cannot trigger change events.

---

[← Back to documentation](../README.md#documentation)
