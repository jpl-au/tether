# Best practices

## Keep Handle fast

Handle runs inside the session's command loop. While it executes, no other events, commands, or effects are processed for that session. The client sees a frozen page.

```go
// Wrong — blocks the loop for the duration of the query
Handle: func(sess tether.PreSession, s State, ev tether.Event) State {
    rows, _ := db.Query("SELECT * FROM items WHERE user_id = ?", s.UserID)
    s.Items = scanItems(rows)
    return s
},
```

Move slow work into a background goroutine and push the result back via `Update`:

```go
Handle: func(sess tether.PreSession, s State, ev tether.Event) State {
    if ev.Action == "refresh" {
        live := sess.(*tether.Session[State])
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

The same applies to `OnConnect` — blocking there delays the session becoming interactive. Use `Session.Go` for database lookups, API calls, or any I/O that might be slow.

## Always key state-dependent elements

The diff engine only tracks elements marked with `.Dynamic("key")`. If state changes but the affected element has no key, the diff produces no patches and the client never updates. This is silent — no error, no warning.

```go
// Wrong — no Dynamic key, changes are invisible to the diff engine
span.Textf("Count: %d", state.Count)

// Right
span.Textf("Count: %d", state.Count).Dynamic("count")
```

Use `Config.OnNoPatch` to detect missing keys in development and production:

```go
OnNoPatch: func(sess *tether.Session[State], info tether.NoPatch) {
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

When the set of Dynamic keys changes between renders — keys added, removed, or reordered — the diff engine falls back to a full root morph. Correct but expensive.

```go
// Wrong — the key disappears when items is empty
if len(items) > 0 {
    return ul.New(nodes...).Dynamic("list")
}
return p.Text("Empty")

// Right — the key is always present
if len(items) == 0 {
    return div.New(p.Text("Empty")).Dynamic("list")
}
return div.New(ul.New(nodes...)).Dynamic("list")
```

Use `Config.OnStructuralChange` to detect unstable key sets. See [stable key sets](server-updates.md#stable-key-sets).

## Use signals for high-frequency updates

A full render-diff-send cycle for a single counter increment is wasteful. Signals push values directly to bound elements — no render, no diff, no HTML:

```go
// In Render — bind the element once
bind.BindText(span.New(), "count")

// From anywhere — push the value
sess.Signal("count", 42)
```

Signals are ideal for counters, progress bars, status indicators, and anything where the DOM change is a single text or attribute update. See [signals](signals.md) for the full guide.

## Prefer SetData in hot render loops

The generic bind helpers (`bind.Click`, `bind.Input`, etc.) are convenient but add ~250ns per element. For most pages this is negligible. For render functions that produce thousands of event-bound elements — large tables, long lists — use `SetData` directly:

```go
// Generic helper — clearer, slightly slower
bind.Click(button.Text("+"), "increment")

// Direct — faster in bulk
button.Text("+").SetData("tether-click", "increment")
```

See [performance](performance.md) for benchmarks.

## Isolate cross-session state

Each session has its own state. Shared mutable state at the package level creates data races — multiple session goroutines read and write it concurrently without synchronisation.

```go
// Wrong — package-level mutable state accessed from multiple sessions
var onlineUsers int

OnConnect: func(sess *tether.Session[State]) {
    onlineUsers++ // data race
},
```

Use `Value` for shared state that sessions observe, `Bus` for discrete events, and `Group` for broadcasting state mutations:

```go
var onlineCount = tether.NewValue(0)

OnConnect: func(sess *tether.Session[State]) {
    onlineCount.Update(func(n int) int { return n + 1 })
    tether.Observe(onlineCount, sess, func(count int, s State) State {
        s.OnlineUsers = count
        return s
    })
},
```

`Value`, `Bus`, and `Group` are all internally synchronised. See [broadcasting](broadcasting.md) for when to use each.

## Use OnConnect for subscriptions

`Observe` and `On` register subscriptions. Call them once in `OnConnect`, not inside Handle:

```go
// Wrong — creates a new subscription on every click event
Handle: func(sess tether.PreSession, s State, ev tether.Event) State {
    live := sess.(*tether.Session[State])
    tether.Observe(onlineCount, live, func(count int, s State) State {
        s.OnlineUsers = count
        return s
    })
    return s
},

// Right — subscribe once when the session connects
OnConnect: func(sess *tether.Session[State]) {
    tether.Observe(onlineCount, sess, func(count int, s State) State {
        s.OnlineUsers = count
        return s
    })
},
```

Subscriptions created in `OnConnect` are cleaned up automatically when the session is destroyed. No manual unsubscribe needed.

## Use Equal to skip unchanged renders

When many events leave state unchanged — keypresses that don't affect the model, button clicks that only trigger side effects — the render and diff are wasted work. Provide an `Equal` function to skip them:

```go
tether.New(tether.Config[State]{
    Equal: func(a, b State) bool {
        return a == b
    },
    // ...
})
```

When `Equal` returns true, the render, diff, and send are skipped entirely. Side effects (Toast, Signal, etc.) are still sent.

For struct types with slice or map fields, `a == b` does not compile — use `reflect.DeepEqual` or write a field-by-field comparison. A manual comparison is faster and avoids reflecting over fields that don't affect rendering.
