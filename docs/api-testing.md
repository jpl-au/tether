# Testing

## tethertest

Test harness for Handle functions:

```go
h := tethertest.New(tethertest.Config[State]{
    State:      State{Count: 0},
    Render:     render,
    Handle:     handle,
    Components: []tether.ComponentMount[State]{
        tether.Mount("likes", getLikes, setLikes),
    },
    Middleware: []tether.Middleware[State]{withAuth},
    OnNavigate: onNavigate,
})
```

| Field | Type | Description |
|-------|------|-------------|
| `State` | `S` | Starting state. Events accumulate onto it |
| `Render` | `tether.RenderFunc[S]` | Optional - required for `HTML()`, `Render()`, `RenderNode()` |
| `Handle` | `func(Session, S, Event) S` | The handler under test |
| `Middleware` | `[]tether.Middleware[S]` | Wraps Handle, as in the real config |
| `OnNavigate` | `func(Session, S, Params) S` | Invoked by `Navigate()` |
| `OnConnect` | `func(Session)` | Invoked by `Connect()` |
| `OnDisconnect` | `func(Session)` | Invoked by `Disconnect()` |
| `Components` | `[]tether.ComponentMount[S]` | Mounted components, dispatched by prefix |
| `Layout` | `func(S, node.Node) node.Node` | Wraps `Render()` output, as in the real config |
| `Session` | `func() *tether.CaptureSession` | Supplies the session per event - set `PushErr` or a cancelled `Ctx` to test those paths |

### Sending events

```go
h.Send("increment")                              // click event
h.SendInput("search", "query")                   // input event with value
h.SendSubmit("save", map[string]string{"n": "v"}) // submit event with form data
h.SendEvent(tether.Event{...})                      // arbitrary event
h.Navigate("/users?id=42")                        // navigate event with URL params
```

### State and render

```go
h.State()       // accumulated state after all events
h.HTML()        // rendered HTML from last Send
h.Render()      // full GET render (initial page load)
h.RenderNode()  // raw node tree for direct inspection
```

### Side-effect accessors

```go
h.Toast()       // last toast text (empty if none)
h.URL()         // last navigation URL
h.Title()       // last title change
h.Announce()    // last screen reader announcement
h.Flash()       // last flash messages (map[string]string)
h.Signals()     // last signal values (map[string]any)
h.ScrollTo()    // last scroll-into-view selector
h.Download()    // last download URL
h.Prefetch()    // last prefetch hints ([]string, nil if none)
h.MorphKeys()   // last targeted morph keys ([]string, nil if none)
```

### Lifecycle

```go
h.Connect()     // trigger OnConnect callback
h.Disconnect()  // trigger OnDisconnect callback
```

Test session registration, presence tracking, and cleanup logic without a real transport.

### Assertion helpers

```go
h.HasToast("Saved")              // toast matches text
h.HasSignal("count", float64(1)) // signal matches key and value
h.HasAnnounce("Done")            // announcement matches text
h.HasFlash("#msg", "Saved")      // flash matches selector and text
h.Replaced()                     // last URL used ReplaceURL (not Navigate)
```

### Middleware

The harness uses the same `tether.Middleware[S]` type as `StatefulConfig` and `StatelessConfig`, wrapping the `Session`-based handler:

```go
type HandleFunc[S any] func(tether.Session, S, tether.Event) S
type Middleware[S any] func(next HandleFunc[S]) HandleFunc[S]
```

---

## Testing components

`tethertest.NewComponent` drives a single [`tether.Component`](components.md)
directly - no page state, no `StatefulConfig`, no prefix routing. Use it to test
a component in isolation; use `Harness.Components` above to test it wired into a
page.

```go
c := tethertest.NewComponent(Counter{Count: 0})

c.Mount()                                   // calls Mount if the component implements Mounter
c.Send("increment")                          // click event, no prefix needed
c.SendInput("search", "query")               // input event with value
c.SendSubmit("save", map[string]string{...}) // submit event with form data
c.SendEvent(tether.Event{...})               // arbitrary event

c.Component()   // the component after all events, typed - no assertion needed
c.HTML()        // the component's rendered HTML
```

Events are dispatched straight to the component's `Handle`, so actions are the
bare names the component declares (`"increment"`), not the prefixed names a
mounted component would receive (`"counter.increment"`).

The same side-effect accessors and assertion helpers as `Harness` are available:
`Toast`, `URL`, `Replaced`, `Title`, `Announce`, `Flash`, `Signals`, `ScrollTo`,
`Download`, `Prefetch`, and the matching `HasToast` / `HasAnnounce` / `HasFlash`
/ `HasSignal`.

```go
c.Send("save")
if !c.HasToast("Saved") {
    t.Error("expected a save confirmation")
}
```

---

[← API reference](api.md)
