# API reference

## Config

`tether.Config[S]` configures a handler. Only `Render` is required — everything else has sensible defaults.

```go
tether.New(tether.Config[State]{
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
| `Handle` | `func(Session, S, Event) S` | Processes events, returns new state |
| `Middleware` | `[]Middleware[S]` | Wraps Handle with cross-cutting behaviour |
| `OnNavigate` | `func(Session, S, Params) S` | Handles URL navigation and initial load |
| `Layout` | `func(S, node.Node) node.Node` | Wraps the tether root in a full HTML document |
| `Equal` | `func(a, b S) bool` | Skips render when state is unchanged |

### Transport

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Upgrade` | `func(w, r) (Transport, error)` | — | Primary transport (typically `ws.Upgrade()`) |
| `Fallback` | `func(w, r) (Transport, error)` | — | Secondary transport (typically `sse.Upgrade()`) |
| `Mode` | `mode.Transport` | `mode.Both` | Which transports to accept |

Mode constants: `mode.HTTP`, `mode.WebSocket`, `mode.ServerSentEvents`, `mode.Both`.

### Lifecycle

| Field | Type | Description |
|-------|------|-------------|
| `OnConnect` | `func(*Session[S])` | Called when a session connects |
| `OnDisconnect` | `func(*Session[S])` | Called when a session is permanently destroyed |
| `OnStructuralChange` | `func(*Session[S], StructuralChange)` | Called when Dynamic keys change between renders |
| `OnNoPatch` | `func(*Session[S], NoPatch)` | Called when a render cycle produces no patches |
| `Groups` | `[]*Group[S]` | Groups the session auto-joins on connect |

`StructuralChange` reports what changed when the diff engine falls back to a root morph:

| Field | Type | Description |
|-------|------|-------------|
| `Added` | `[]string` | Keys in the new tree but not the old |
| `Removed` | `[]string` | Keys in the old tree but not the new |
| `Reordered` | `bool` | Same keys but different order |
| `Bytes` | `int` | Size of the re-rendered HTML sent as a root morph |

`NoPatch` describes a render cycle that produced no DOM changes:

| Field | Type | Description |
|-------|------|-------------|
| `Source` | `string` | `"update"`, `"navigate"`, or `"event"` |
| `Action` | `string` | Event action; empty for `"update"` source |

When either callback is configured, the framework's own logging for that event is suppressed — the callback controls the output. When nil and DevMode is active, a debug message is logged instead.

### Timeouts

`Config.Timeouts` groups duration-based settings:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Idle` | `time.Duration` | 0 (disabled) | Close sessions inactive for this long |
| `MaxLifetime` | `time.Duration` | 0 (disabled) | Close sessions after this long regardless |
| `Reconnect` | `time.Duration` | 30s | Keep disconnected sessions alive for reconnection |
| `DisableReconnect` | `bool` | false | Destroy sessions immediately on disconnect |
| `Pending` | `time.Duration` | 30s | Wait for browser to claim pre-warmed session |
| `Heartbeat` | `time.Duration` | 20s | SSE keep-alive interval |
| `DisableHeartbeat` | `bool` | false | Stop SSE keep-alive comments |
| `ShutdownGrace` | `time.Duration` | 10s | Grace period for `ListenAndServe` shutdown |
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
| `FlashDuration` | `time.Duration` | 5s | Flash message auto-clear duration |
| `ToastDuration` | `time.Duration` | 5s | Toast notification auto-dismiss duration |
| `BackgroundSync` | `bool` | false | Queue failed SSE events in IndexedDB for replay |

### Extensions

| Field | Type | Description |
|-------|------|-------------|
| `Upload` | `*UploadConfig[S]` | File upload support (see [extensions](extensions.md)) |
| `Push` | `*PushConfig[S]` | Web Push notifications (see [push](push-notifications.md)) |
| `Worker` | `bool` | Enable service worker (auto-enabled by Push) |
| `Assets` | `[]*Asset` | Embedded asset collections — auto-served with content-hashed URLs |
| `DevMode` | `bool` | Development mode (or set `TETHER_DEV=1`). See [operations](operations.md#dev-mode) |
| `WireFormat` | `wire.Format` | Encoding for server-to-client updates (default `wire.JSON`) |

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

`*tether.LiveSession[S]` is the handle for an active connection. Methods are safe to call from any goroutine.

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
s.SignalBatch("count", 42, "status", "online")  // flat key-value pairs
s.Push(push.Notification{...})         // Web Push notification
```

### Session (interface)

`tether.Session` is a non-generic interface exposing LiveSession's side-effect methods without the state type parameter. It is available in `Handle`, `OnNavigate`, stateless page handlers, and reusable components.

Because Session has no generic parameter, component handlers can accept it directly — they don't need to know the application's state type:

```go
func todoHandle(sess tether.Session, ts TodoState, ev tether.Event) TodoState {
    sess.Toast("Saved")   // works — no generic needed
    sess.Signal("count", len(ts.Items))
    return ts
}
```

Methods: `ID`, `Context`, `Go`, `Toast`, `Navigate`, `ReplaceURL`, `SetTitle`, `Announce`, `Flash`, `Signal`, `Signals`, `SignalBatch`, `Push`, `Close`.

`ID` returns an empty string in stateless page mode (PageConfig) — there is no persistent session. `Push` returns an error during pre-warming (initial GET) since no browser subscription exists yet. `Close` terminates the session's transport; in stateless page mode and tethertest it is a no-op. During live sessions all methods work normally.

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

Event type constants live in the `event` package: `event.Click`, `event.Input`, `event.Submit`, `event.Change`, `event.KeyDown`, `event.Focus`, `event.Blur`, `event.Navigate`, `event.Viewport`, `event.Online`, `event.Offline`, `event.AppInstalled`. Create custom types with `event.Custom("name")`.

### Typed data extraction

```go
count, _ := ev.Int("count")
if ev.Bool("confirmed") { ... }

var form struct {
    Email string `tether:"email"`
    Age   int    `tether:"age"`
}
ev.Bind(&form)
```

### Params

```go
type Params struct {
    Path  string     // URL path
    Query url.Values // parsed query parameters
}
```

Params provides typed extraction helpers that mirror `Event`'s API for
consistency — one data extraction pattern across the framework.

**Single-value helpers** (return error on missing/invalid):

```go
p.Get("q")              // string — first value for key
p.Int("page")           // (int, error)
p.Float64("min")        // (float64, error)
p.Bool("active")        // true only for "true"
```

**Soft getters** (return default on missing/invalid — ideal for optional
URL parameters):

```go
p.IntOr("page", 1)      // int — returns 1 if missing or invalid
p.Float64Or("min", 0.0) // float64
p.BoolOr("drafts", false) // bool — returns default if key absent
```

**Multi-value helpers** (for repeated keys like `?tag=go&tag=web`):

```go
p.Strings("tag")        // []string — all values for key
p.Ints("id")            // ([]int, error)
p.Float64s("v")         // ([]float64, error)
```

---

## Handler

`*tether.Handler[S]` implements `http.Handler`.

```go
h := tether.New(cfg)
mux.Handle("/app", h)

h.Health()          // HealthStatus{Pending, Active, Disconnected}
h.Drain(ctx)        // stop new sessions, wait for existing
h.Shutdown(ctx)     // close all sessions
```

### ListenAndServe

For full-page apps, `ListenAndServe` handles signal trapping, graceful shutdown, and sensible defaults:

```go
h.ListenAndServe("")                      // checks PORT env, defaults to :8080
h.ListenAndServe(":3000")                 // explicit address
h.ListenAndServe("", existingMux)         // mount on an existing mux
h.ListenAndServeTLS("", "cert.pem", "key.pem")  // HTTPS, defaults to :443
```

On `SIGINT` or `SIGTERM`, sessions are drained gracefully (up to `Timeouts.ShutdownGrace`, default 10s) before the process exits. When an existing `http.Handler` is passed, the handler is mounted on it alongside the client JS runtime and any configured assets.

---

## Group

Broadcast state changes to multiple sessions:

```go
group := tether.NewGroup[State]()
group.Add(sess)
group.Remove(sess)
group.Len()       // member count
group.All()       // iter.Seq[*LiveSession[S]]

group.Broadcast(func(target *tether.LiveSession[State], s State) State {
    s.Message = "hello"
    return s
})

group.BroadcastOthers(sender, func(target *tether.LiveSession[State], s State) State {
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

tether.New(tether.Config[State]{
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
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
bind.On(el, "dblclick", "action")  // arbitrary DOM event
bind.Viewport(el, "action")        // viewport enter (infinite scroll)
bind.Collect(el, "#selector")      // collect input values at click time
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
bind.Reset(el)                   // reset form fields after submit
bind.AutoFocus(el)               // focus after next server update
bind.Indicator(el, "#spinner")   // show loading indicator at selector
bind.FocusTrap(el)               // trap Tab within descendants
```

### Uploads

```go
bind.Upload(el, "avatar")              // trigger file upload
bind.UploadInput(el, "#avatar-input")  // CSS selector for distant file inputs
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

Every nested helper has a `With*` Apply option. Server events:

```go
bind.OnClick("act")         bind.OnSubmit("act")
bind.OnInput("act")         bind.OnChange("act")
bind.OnKeyDown("act")       bind.OnFocus("act")
bind.OnBlur("act")          bind.OnViewport("act")
bind.WithEvent("dblclick", "act")
bind.WithCollect("#selector")
```

Control:

```go
bind.WithDisable("...")     bind.WithConfirm("...")
bind.WithPreserve()         bind.WithAutoFocus()
bind.WithIndicator("#el")   bind.WithFocusTrap()
```

Timing:

```go
bind.WithDebounce(150*time.Millisecond)
bind.WithThrottle(time.Second)
bind.WithFilterKey("Enter")
bind.WithEventData("key", "val")
```

Directives:

```go
bind.WithLink()             bind.WithCloak()
bind.WithPermanent()        bind.WithToggleClass("cls")
bind.WithToggleTarget("#x") bind.WithToggleAttr("hidden")
```

Signal bindings:

```go
bind.WithBindText("count")           bind.WithBindShow("isOpen")
bind.WithBindHide("isOpen")          bind.WithBindClass("active", "sel")
bind.WithBindAttr("disabled", "busy") bind.WithBindValue("email")
```

Signal directives:

```go
bind.WithToggleSignal("menuOpen")
bind.WithSetSignal("tab", "settings")
bind.WithOptimistic("liked", "true")
bind.WithOptimisticToggle("liked")
```

Upload:

```go
bind.WithUpload("avatar")
bind.WithUploadInput("#avatar-input")
bind.WithUploadProgress("avatar")
```

Lifecycle:

```go
bind.WithHook("chart")      bind.WithTransition("fade")
```

Escape hatch for custom attributes:

```go
bind.WithData("tether-custom", "value")  // data-tether-custom="value"
```

---

## Middleware

Wraps `Handle` for cross-cutting concerns. Applied outermost-first:

```go
type Middleware[S any] func(HandleFunc[S]) HandleFunc[S]

tether.New(tether.Config[State]{
    Middleware: []tether.Middleware[State]{withLogging, withAuth},
})
```

---

## Catch

Render-level error boundary:

```go
tether.Catch(func() node.Node {
    return riskyWidget(s)
}, span.Text("Unavailable"))
```

Recovers panics, logs them, and returns the fallback node.

---

## tethertest

Test harness for Handle functions:

```go
h := tethertest.New(tethertest.Config[State]{
    State:      State{Count: 0},
    Render:     render,
    Handle:     handle,
    Middleware: []tethertest.Middleware[State]{withAuth},
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
h.URLWasReplaced()               // last URL used ReplaceURL (not Navigate)
```

### Middleware

`tethertest.Middleware` wraps the `Session`-based handler used by tethertest and `PageConfig`:

```go
type HandleFunc[S any] func(tether.Session, S, tether.Event) S
type Middleware[S any] func(next HandleFunc[S]) HandleFunc[S]
```

---

## Bus

Typed pub/sub for cross-session communication. Create one per event type at program startup and share it across handlers:

```go
var messages = tether.NewBus[MessageSent]()
```

### Publishing

```go
bus.Publish(msg)         // to all subscribers — use for external sources (DB, queues, cron)
bus.Emit(sess, msg)      // to all except sender — use inside Handle
bus.Len()                // active subscriber count
```

`Emit` accepts any `Session` value, so it can be called directly from `Handle` without a type-assert. In live sessions, publication is enqueued on the sender's command loop so the sender's diff is sent to the client before other subscribers react. Subscriptions registered via `tether.On` whose session ID matches the emitting session are automatically skipped — preventing double-apply since `Handle` already updated the sender's state.

### Subscribing

Raw subscription for non-session consumers (external services, monitoring):

```go
// Synchronous — callback runs in the publisher's goroutine. Must not block.
cancel := bus.Subscribe(ctx, func(msg ChatMessage) { ... })

// Asynchronous — callback runs in its own goroutine per event. Safe for I/O.
cancel := bus.SubscribeAsync(ctx, func(msg ChatMessage) { ... })
```

`Subscribe` runs the callback synchronously in the publisher's goroutine — it must not block. `SubscribeAsync` spawns a goroutine per event, isolating the publisher from slow callbacks. Use `SubscribeAsync` for external consumers that perform database writes, HTTP calls, or other I/O.

Session-aware subscription via `tether.On` — the primary way to connect a Bus to a session:

```go
tether.On(sess, messages, func(msg MessageSent, state ChatState) ChatState {
    state.Messages = append(state.Messages, msg.Text)
    return state
})
```

`tether.On` subscribes the session to the bus. When an event arrives, the callback runs inside the session's command loop (via `Session.Update`) with the event and the current state. The callback returns the new state — same pattern as Update.

Key behaviours:
- **Sender filtering** — if the event was emitted by this session (via `Bus.Emit`), the callback is skipped automatically
- **Auto-cleanup** — the subscription is removed when the session is destroyed (context cancelled)
- **Thread-safe** — the callback runs on the session's command loop, never concurrently with Handle or other Updates

Typical usage is in `OnConnect`:

```go
OnConnect: func(sess *tether.LiveSession[State]) {
    tether.On(sess, activityBus, func(item ActivityItem, s State) State {
        s.Activity = append(s.Activity, item)
        return s
    })
},
```

### Bus vs Group

Bus is parameterised on the **event type** — any session can subscribe regardless of its state type. Group requires all sessions to share the same state type. Use Bus for cross-handler communication; use Group for same-handler broadcasting.

---

## Value

Shared observable state that notifies sessions when it changes. Built on top of Bus internally:

```go
var onlineCount = tether.NewValue(0)
```

### Reading and writing

```go
v.Load()              // lock-free read — safe from any goroutine
v.Store(val)          // set and notify all observers
v.Update(func(V) V)   // atomic read-modify-write (counters, accumulators)
v.Len()               // active observer count
```

### Observing

`tether.Observe` subscribes a session to a Value. The current value is delivered immediately so the session's state is up to date from the moment of subscription. Future changes via Store or Update are delivered automatically:

```go
tether.Observe(sess, onlineCount, func(count int, s State) State {
    s.OnlineUsers = count
    return s
})
```

Key behaviours:
- **Immediate sync** — the callback fires once with the current value at subscription time
- **Atomic subscribe+read+apply** — the subscription, read, and initial state application happen within a single session command, so a concurrent Store is always ordered after the initial value
- **Auto-cleanup** — removed when the session is destroyed
- **Thread-safe** — runs on the session's command loop

Typical usage is in `OnConnect`:

```go
OnConnect: func(sess *tether.LiveSession[State]) {
    tether.Observe(sess, onlineCount, func(count int, s State) State {
        s.OnlineCount = count
        return s
    })
},
```

### Value vs Bus

Use Value for state that multiple sessions need to stay in sync with (online counts, shared configuration, room membership). Use Bus for discrete domain events (chat messages, notifications, activity feeds).

---

## Scope

Focus session state onto a component:

```go
sc := tether.Scope[State, FormState]{
    View:   func(s State) FormState { return s.Form },
    Update: func(s State, f FormState) State { s.Form = f; return s },
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

**Sentinel errors** — check with `errors.Is()`:

| Error | Meaning |
|-------|---------|
| `tether.ErrPushNotConfigured` | Handler created without `PushConfig` |
| `tether.ErrPushNoSubscription` | Browser has not registered a push subscription |
| `tether.ErrPushPreWarm` | Push called during pre-warming (no browser yet) |
| `push.ErrSubscriptionExpired` | Push service returned HTTP 410 |
