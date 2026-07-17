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

[← API reference](api.md)
