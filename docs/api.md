# API reference

## Config

`poly.Config[S]` configures a handler. Only `Render` is required — everything else has sensible defaults.

```go
poly.New(poly.Config[State]{
    Upgrade:      ws.Upgrade(),
    InitialState: func(r *http.Request) State { return State{} },
    Render:       render,
    Handle:       handle,
})
```

### Core

| Field | Type | Description |
|-------|------|-------------|
| `InitialState` | `func(*http.Request) S` | Creates initial state per connection |
| `Render` | `func(S) node.Node` | Builds the node tree from state |
| `Handle` | `func(*Session[S], S, Event) S` | Processes events, returns new state |
| `Middleware` | `[]Middleware[S]` | Wraps Handle with cross-cutting behaviour |
| `OnNavigate` | `func(PreSession, S, Params) S` | Handles URL navigation and initial load |
| `Layout` | `func(S, node.Node) node.Node` | Wraps the poly root in a full HTML document |
| `Equal` | `func(a, b S) bool` | Skips render when state is unchanged |

### Transport

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Upgrade` | `func(w, r) (Transport, error)` | — | Primary transport (typically `ws.Upgrade()`) |
| `Fallback` | `func(w, r) (Transport, error)` | — | Secondary transport (typically `sse.Upgrade()`) |
| `Mode` | `mode.Transport` | `mode.WebSocket` | Which transports to accept |

Mode constants: `mode.WebSocket`, `mode.SSE`, `mode.Auto`, `mode.Fetch`.

### Lifecycle

| Field | Type | Description |
|-------|------|-------------|
| `OnConnect` | `func(*Session[S])` | Called when a session connects |
| `OnDisconnect` | `func(*Session[S])` | Called when a session is permanently destroyed |
| `OnStructuralChange` | `func(*Session[S], StructuralChange)` | Called when DOM keys change (telemetry) |
| `Groups` | `[]*Group[S]` | Groups the session auto-joins on connect |

### Timeouts

`Config.Timeouts` groups duration-based settings:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Idle` | `time.Duration` | 0 (disabled) | Close sessions inactive for this long |
| `MaxLifetime` | `time.Duration` | 0 (disabled) | Close sessions after this long regardless |
| `Reconnect` | `time.Duration` | 30s | Keep disconnected sessions alive for reconnection |
| `Pending` | `time.Duration` | 30s | Wait for browser to claim pre-warmed session |
| `Heartbeat` | `time.Duration` | 20s | SSE keep-alive interval (-1 disables) |
| `Retry` | `time.Duration` | 1s | Initial client reconnection delay |
| `MaxRetry` | `time.Duration` | 30s | Maximum exponential backoff |

### Limits

`Config.Limits` groups capacity constraints:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `MaxSessions` | `int` | 0 (unlimited) | Maximum concurrent sessions |
| `CmdBufferSize` | `int` | 64 | Session command channel capacity |
| `MaxEventBytes` | `int64` | 64 KB | Maximum POST event body size |

### Client

`Config.Client` groups browser-side settings:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DefaultDebounce` | `time.Duration` | 300ms | Default input event debounce |
| `TransitionTimeout` | `time.Duration` | 5s | Fallback for CSS `transitionend` |

### Extensions

| Field | Type | Description |
|-------|------|-------------|
| `Upload` | `*UploadConfig[S]` | File upload support (see [extensions](extensions.md)) |
| `Push` | `*PushConfig[S]` | Web Push notifications (see [push](push-notifications.md)) |
| `Worker` | `bool` | Enable service worker (auto-enabled by Push) |
| `Precache` | `[]string` | Extra URLs for service worker to cache |
| `DevMode` | `bool` | Development mode (or set `POLY_DEV=1`) |

### Security

`Config.Security` groups CSRF protection:

| Field | Type | Description |
|-------|------|-------------|
| `AllowedOrigins` | `[]string` | Restrict connections to these origins |

### Other

| Field | Type | Description |
|-------|------|-------------|
| `Logger` | `*slog.Logger` | Session error logger (default `slog.Default()`) |

---

## Session

`*poly.Session[S]` is the handle for an active connection. Methods are safe to call from any goroutine.

### State

```go
s.State()                       // read current state
s.Update(func(s State) State {  // mutate state from outside Handle
    s.Count++
    return s
})
s.ID()                          // unique session identifier
s.Context()                     // cancelled on permanent destruction
s.Go(func(ctx context.Context) { ... }) // goroutine bound to session lifetime
s.Close()                       // close the transport connection
```

### Side effects

Call these inside `Handle` to buffer effects into the same update message, or from any goroutine for standalone updates:

```go
s.Toast("Settings saved")              // global notification
s.Announce("Item added to cart")       // screen reader live region
s.Flash("#notice", "Saved")            // notification at selector (5s)
s.Navigate("/success")                 // pushState
s.ReplaceURL("/current?saved=1")       // replaceState
s.SetTitle("Settings — My App")        // document.title
s.Signal("count", 42)                  // push reactive value
s.Signals(map[string]any{"a": 1})      // push multiple values
s.Push(push.Notification{...})         // Web Push notification
```

### PreSession

`poly.PreSession` is the subset of Session available in `OnNavigate` and stateless page handlers. It exposes: `ID`, `Toast`, `Navigate`, `ReplaceURL`, `SetTitle`, `Announce`, `Flash`, `Signal`, `Signals`.

---

## Event

```go
type Event struct {
    Type    event.Type        // Click, Input, Submit, Change, KeyDown, etc.
    Action  string            // application-defined action name
    Data    map[string]string // event-specific key-value pairs
    EventID string            // monotonic counter for correlation
}

// Accessors
func (e Event) Value() string                      // e.Data["value"]
func (e Event) Key() string                        // keydown key name
func (e Event) Get(key string) (string, bool)      // check and get
func (e Event) Int(key string) (int, error)        // parse integer
func (e Event) Float64(key string) (float64, error) // parse float
func (e Event) Bool(key string) bool               // "true" → true
func (e Event) Bind(dest any) error                // struct-tag form binding
```

Event type constants live in the `event` package: `event.Click`, `event.Input`, `event.Submit`, `event.Change`, `event.KeyDown`, `event.Focus`, `event.Blur`, `event.Navigate`. Create custom types with `event.Custom("name")`.

### Typed data extraction

```go
count, _ := ev.Int("count")
if ev.Bool("confirmed") { ... }

var form struct {
    Email string `poly:"email"`
    Age   int    `poly:"age"`
}
ev.Bind(&form)
```

### Params

```go
type Params struct {
    Path  string     // URL path
    Query url.Values // query parameters
}
```

---

## Handler

`*poly.Handler[S]` implements `http.Handler`.

```go
h := poly.New(cfg)
mux.Handle("/app", h)

h.Health()          // HealthStatus{Pending, Active, Disconnected}
h.Drain(ctx)        // stop new sessions, wait for existing
h.Shutdown(ctx)     // close all sessions
```

---

## Group

Broadcast state changes to multiple sessions:

```go
group := poly.NewGroup[State]()
group.Add(sess)
group.Remove(sess)
group.Len()       // member count
group.All()       // iter.Seq[*Session[S]]

group.Broadcast(func(target *poly.Session[State], s State) State {
    s.Message = "hello"
    return s
})

group.BroadcastOthers(sender, func(target *poly.Session[State], s State) State {
    s.Message = "hello"
    return s
})
```

Optional callbacks: `group.OnJoin`, `group.OnLeave`.

For auto-registration, pass groups via `Config.Groups`.

---

## Router

Dispatch `Render` and `Handle` by URL path:

```go
r := router.New[State](func(s State) string { return s.Page })
r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
r.Route("/settings", router.Page[State]{Render: settingsRender})
r.NotFound(router.Page[State]{Render: notFoundRender})

poly.New(poly.Config[State]{
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, path string) { s.Page = path }),
})
```

---

## Bind helpers

All helpers live in the `bind` package and work with any Fluent element type.

### Server events

```go
bind.Click(el, "action")           // click
bind.Submit(el, "action")          // form submit (includes field values)
bind.Input(el, "action")           // input (debounced at 300ms)
bind.Change(el, "action")          // select/checkbox/radio commit
bind.KeyDown(el, "action")         // keydown (modifiers in Data)
bind.FilterKey(el, "Enter")        // restrict keydown to specific key
bind.Focus(el, "action")           // focus
bind.Blur(el, "action")            // blur
bind.Viewport(el, "action")        // viewport enter (infinite scroll)
bind.EventData(el, "key", "val")   // attach extra data to events
bind.Debounce(el, 150*time.Millisecond) // override debounce
bind.Throttle(el, time.Second)          // minimum event interval
```

### Signal bindings

```go
bind.BindText(el, "count")            // set textContent from signal
bind.BindShow(el, "isOpen")           // show when truthy
bind.BindHide(el, "isOpen")           // hide when truthy
bind.BindClass(el, "active", "sel")   // toggle class from signal
bind.BindAttr(el, "disabled", "busy") // set attribute from signal
bind.BindValue(el, "email")           // set form value from signal
```

### Client directives

```go
bind.Link(el)                          // client-side navigation
bind.ToggleClass(el, "is-open")        // toggle class on click
bind.ToggleTarget(el, "#nav")          // direct toggle at selector
bind.ToggleAttr(el, "hidden")          // toggle attribute on click
bind.ToggleSignal(el, "menuOpen")      // toggle boolean signal
bind.SetSignal(el, "tab", "settings")  // set signal to value
bind.Optimistic(el, "liked", "true")   // optimistic signal update
bind.OptimisticToggle(el, "liked")     // optimistic signal toggle
bind.Cloak(el)                         // hide until runtime ready
bind.Permanent(el)                     // exclude from morphing
```

### Control

```go
bind.Disable(el, "Saving...")    // disable during event, optional text swap
bind.Confirm(el, "Are you sure?") // confirmation prompt before send
bind.Preserve(el)                // prevent form reset after submit
bind.AutoFocus(el)               // focus after next server update
bind.Indicator(el, "#spinner")   // show loading indicator at selector
bind.FocusTrap(el)               // trap Tab within descendants
```

### Uploads

```go
bind.Upload(el, "avatar")              // trigger file upload
bind.UploadProgress(el, "avatar")      // bind to upload progress signal
```

### Lifecycle

```go
bind.Hook(el, "chart")           // JS lifecycle callbacks
bind.Transition(el, "fade")      // CSS enter/leave transitions
```

### Composition with Apply

Stack multiple behaviours top-to-bottom instead of nested inside-out:

```go
bind.Apply(btn,
    bind.OnClick("delete"),
    bind.WithConfirm("Sure?"),
    bind.WithDisable("Deleting..."),
)
```

---

## Middleware

Wraps `Handle` for cross-cutting concerns. Applied outermost-first:

```go
type Middleware[S any] func(HandleFunc[S]) HandleFunc[S]

poly.New(poly.Config[State]{
    Middleware: []poly.Middleware[State]{withLogging, withAuth},
})
```

---

## Catch

Render-level error boundary:

```go
poly.Catch(func() node.Node {
    return riskyWidget(s)
}, span.Text("Unavailable"))
```

Recovers panics, logs them, and returns the fallback node.

---

## polytest

Test harness for Handle functions:

```go
h := polytest.New(polytest.Config[State]{
    State:  State{Count: 0},
    Render: render,
    Handle: handle,
})

h.Send("increment")
h.SendInput("search", "query")
h.SendSubmit("save", map[string]string{"name": "Bob"})

h.State()       // accumulated state
h.HTML()        // rendered HTML
h.Toast()       // last toast
h.URL()         // last navigation
h.Title()       // last title change
h.Render()      // full GET render
```

---

## Bus

Typed pub/sub for cross-session communication:

```go
bus := poly.NewBus[ChatMessage]()
bus.Publish(msg)                              // to all subscribers
bus.Emit(sess, msg)                           // to all except sender
cancel := bus.Subscribe(ctx, func(msg ChatMessage) { ... })
poly.On(bus, sess, func(msg ChatMessage, s State) State { ... })
```

---

## Value

Shared observable state:

```go
v := poly.NewValue(initial)
v.Get()                      // lock-free read
v.Set(val)                   // set and notify observers
v.Update(func(V) V)          // atomic read-modify-write
poly.Observe(v, sess, func(val V, s State) State { ... })
```

---

## Scope

Focus session state onto a component:

```go
sc := poly.Scope[State, FormState]{
    Get: func(s State) FormState { return s.Form },
    Set: func(s State, f FormState) State { s.Form = f; return s },
}

// In Handle:
return sc.Handle(sess, s, ev, formHandle)

// In Update:
sess.Update(func(s State) State {
    return sc.With(s, func(f FormState) FormState { f.Valid = true; return f })
})
```

---

## Push

Web Push notifications via the `push` package:

```go
sender := push.NewSender(push.Config{
    VAPIDPublicKey:  pub,
    VAPIDPrivateKey: priv,
    Subject:         "mailto:admin@example.com",
})

sess.Push(push.Notification{
    Title: "New message",
    Body:  "From Alice",
    URL:   "/messages",
})

// Generate new VAPID keys
pub, priv, err := push.GenerateVAPIDKeys()
```
