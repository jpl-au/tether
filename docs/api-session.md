# Session

## Session (interface)

`tether.Session` is the non-generic interface that `Handle`, `OnNavigate`, stateless page handlers, and reusable components receive. It provides side-effect methods that work identically in stateful mode, stateless mode, and tests - without requiring the application's state type parameter.

### Side effects

Call these inside `Handle` to buffer effects into the same update message, or from any goroutine for standalone updates:

```go
sess.Toast("Settings saved")              // global notification
sess.Announce("Item added to cart")       // screen reader live region
sess.Flash("#notice", "Saved")            // notification at selector (5s)
sess.ScrollTo("#new-item")               // smooth scroll element into view
sess.Download("/export/report.csv")      // trigger file download via HTTP
sess.Navigate("/success")                 // pushState
sess.ReplaceURL("/current?saved=1")       // replaceState
sess.SetTitle("Settings - My App")        // document.title
sess.Signal("count", 42)                  // push reactive value
sess.Signals(map[string]any{"a": 1})      // push multiple values
sess.Prefetch("/checkout")                // hint the browser to prefetch likely-next URLs
sess.Push(push.Notification{...})         // Web Push notification
sess.Morph("count", "title")             // targeted morphs (stateless only)
```

### Other interface methods

```go
sess.ID()                                  // unique session identifier
sess.Context()                             // session lifetime context
sess.Go(func(ctx context.Context) { ... }) // goroutine bound to transport
sess.Close()                               // close the transport connection
```

`ID` returns an empty string in stateless page mode (StatelessConfig) - there is no persistent session. `Push` returns an error during pre-warming (initial GET) since no browser subscription exists yet. `Close` terminates the session's transport; in stateless page mode and tethertest it is a no-op. `Morph` declares targeted Dynamic keys for stateless responses (see [stateless pages](stateless.md#targeted-morphs)); in stateful mode the differ handles targeting automatically, so `Morph` is a no-op with a dev warning.

Because Session has no generic parameter, component handlers can accept it directly:

```go
func todoHandle(sess tether.Session, ts TodoState, ev tether.Event) TodoState {
    sess.Toast("Saved")   // works - no generic needed
    sess.Signal("count", len(ts.Items))
    return ts
}
```

---

## StatefulSession

`*tether.StatefulSession[S]` extends Session with state mutation methods. It is the concrete type behind the Session interface in stateful mode. You receive it directly in lifecycle callbacks (`OnConnect`, `OnDisconnect`, `OnRestore`, `OnPanic`). In `Handle` it arrives as the `tether.Session` interface - type-assert when you need the extended methods.

### State mutation

```go
s.State()                       // read current state (atomic snapshot)
s.Update(func(s State) State {  // mutate state, re-render, send patches
    s.Count++
    return s
})
s.Patch("row-47", func(s State) (State, node.Node) {  // targeted single-key update
    s.Items[47].Count++
    return s, renderRow(s.Items[47])
})
```

### Type assertion from Handle

`Handle` receives `tether.Session` so that component handlers stay non-generic. When you need `Update`, `State`, or `Patch` from within Handle, type-assert to the concrete session:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    live := sess.(*tether.StatefulSession[State])
    live.Go(func(ctx context.Context) {
        result, _ := expensiveWork(ctx)
        live.Update(func(s State) State {
            s.Result = result
            return s
        })
    })
    s.Status = "processing"
    return s
},
```

This pattern is the standard way to run background work from an event handler. The `Go` goroutine is bound to the transport lifetime - it is cancelled when the client disconnects. `Update` is safe to call from any goroutine and serialises through the session's command loop, so it never races with `Handle` or other updates.

See [best practices: Keep Handle fast](best-practices.md#keep-handle-fast) for more detail on when and why to move work out of Handle.

---

[← API reference](api.md)
