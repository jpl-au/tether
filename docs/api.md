# API reference

## App

`tether.App` holds application-wide configuration shared across handlers. Pass it as the first argument to `tether.Stateful` and `tether.Stateless`.

```go
app := tether.App{
    DevMode:  true,
    Assets:   []*tether.Asset{assets},
    Logger:   slog.New(slog.NewJSONHandler(os.Stderr, nil)),
    Client:   tether.Client{DefaultDebounce: 200 * time.Millisecond},
    Security: tether.Security{TrustedOrigins: []string{"https://example.com"}},
}
```

| Field | Type | Description |
|-------|------|-------------|
| `DevMode` | `bool` | Development mode (or set `TETHER_DEV=1`). See [operations](operations.md#dev-mode) |
| `Logger` | `*slog.Logger` | When nil, creates a text handler at INFO level (DEBUG in DevMode) and sets it as the process default once. When provided, used for this handler without touching the global default |
| `Assets` | `[]*Asset` | Asset collections (embedded or filesystem) - auto-served with content-hashed URLs. Set `WatchDir` on filesystem assets for automatic rehashing on file change |
| `Client` | `Client` | Browser-side settings (debounce, transitions, flash duration, etc.) |
| `Security` | `Security` | CSRF protection and session binding settings |

---

## StatefulConfig

`tether.StatefulConfig[S]` configures a handler. `InitialState`, `Render`, and `Handle` are required, plus a transport (`Upgrade` and/or `Fallback` depending on `Mode`). Everything else has sensible defaults.

```go
tether.Stateful(app, tether.StatefulConfig[State]{
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
| `OnNavigate` | `func(Session, S, Params) S` | Handles URL navigation and initial load. Redirects via `Navigate()` are resolved inline (no client round-trip). |
| `Layout` | `func(S, node.Node) node.Node` | Wraps the tether root in a full HTML document |
| `Equal` | `func(a, b S) bool` | Skips render when state is unchanged |

### Transport

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Upgrade` | `func(w, r) (Transport, error)` | - | Primary transport (typically `ws.Upgrade()`) |
| `Fallback` | `func(w, r) (Transport, error)` | - | Secondary transport (typically `sse.Upgrade()`) |
| `Mode` | `mode.Transport` | `mode.Both` | Which transports to accept |

Mode constants: `mode.HTTP`, `mode.WebSocket`, `mode.ServerSentEvents`, `mode.Both`.

### Lifecycle

| Field | Type | Description |
|-------|------|-------------|
| `OnConnect` | `func(*StatefulSession[S])` | Called when a session connects |
| `OnDisconnect` | `func(*StatefulSession[S])` | Called when the transport closes (temporary disconnect or permanent destruction) |
| `OnStructuralChange` | `func(*StatefulSession[S], StructuralChange)` | Called when Dynamic keys change between renders |
| `OnNoPatch` | `func(*StatefulSession[S], NoPatch)` | Called when a render cycle produces no patches |
| `Groups` | `[]*Group[S]` | Groups the session auto-joins on connect |
| `Watchers` | `[]Watcher[S]` | Declarative subscriptions to Value and Bus |
| `Components` | `[]ComponentMount[S]` | Declarative component mounts - events are dispatched by prefix |

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

When either callback is configured, the framework's own logging for that event is suppressed - the callback controls the output. When nil and DevMode is active, a debug message is logged instead.

### Timeouts

`StatefulConfig.Timeouts` groups duration-based settings:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Idle` | `time.Duration` | 0 (disabled) | Close sessions with no activity for this long. Activity includes client events, Update calls, and server-push effects (Signal, Toast, etc.) |
| `MaxLifetime` | `time.Duration` | 0 (disabled) | Close sessions after this long regardless |
| `Reconnect` | `time.Duration` | 30s | Keep disconnected sessions alive for reconnection |
| `DisableReconnect` | `bool` | false | Destroy sessions immediately on disconnect |
| `Pending` | `time.Duration` | 30s | Wait for browser to claim pre-warmed session |
| `Heartbeat` | `time.Duration` | 20s | Keep-alive interval (SSE comments, WebSocket pings) |
| `DisableHeartbeat` | `bool` | false | Stop transport keep-alive frames |
| `ShutdownGrace` | `time.Duration` | 10s | Grace period for `ListenAndServe` shutdown |
| `PendingCheck` | `time.Duration` | 10s | How often the background goroutine scans for expired pending sessions |
| `Retry` | `time.Duration` | 1s | Initial client reconnection delay |
| `MaxRetry` | `time.Duration` | 30s | Maximum exponential backoff |

### Limits

`StatefulConfig.Limits` groups capacity constraints:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `MaxSessions` | `int` | 0 (unlimited) | Maximum concurrent sessions (pending + active + disconnected) |
| `MaxPending` | `int` | 128 | Maximum pre-warmed sessions awaiting transport connection |
| `CmdBufferSize` | `int` | 64 | Session command channel capacity |
| `MaxEventBytes` | `int64` | 64 KB | Maximum POST event body size |
| `MaxPushSubscriptionBytes` | `int64` | 4 KB | Maximum push subscription body size |
| `MaxStateBytes` | `int64` | 0 (disabled) | Warn when serialised session state exceeds this size (save still proceeds) |
| `MaxNavigateRedirects` | `int` | 5 | Maximum consecutive server-side redirects per navigate event |

### Client

`App.Client` groups browser-side settings:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DefaultDebounce` | `time.Duration` | 300ms | Default input event debounce |
| `TransitionTimeout` | `time.Duration` | 5s | Fallback for CSS `transitionend` |
| `FlashDuration` | `time.Duration` | 5s | Flash message auto-clear duration |
| `ToastDuration` | `time.Duration` | 5s | Toast notification auto-dismiss duration |
| `BackgroundSync` | `bool` | false | Queue failed SSE events in IndexedDB for replay |
| `SyncRetention` | `time.Duration` | 1h | How long queued events survive before expiry |

### Extensions

| Field | Type | Description |
|-------|------|-------------|
| `Upload` | `*UploadConfig[S]` | File upload support (see [extensions](extensions.md)) |
| `Push` | `*PushConfig[S]` | Web Push notifications (see [push](push-notifications.md)) |
| `Worker` | `bool` | Enable service worker (auto-enabled by Push) |
| `DiffStore` | `DiffStore` | External persistence for disconnected session snapshots (opt-in, nil by default). See [store](store.md) |
| `SessionStore` | `SessionStore` | External persistence for session state - enables crash recovery and node migration (opt-in, nil by default). See [session-store](session-store.md) |
| `Codec` | `SessionCodec[S]` | Custom serialisation for state `S` (nil = CBOR). Only used when SessionStore is set |
| `OnRestore` | `func(*StatefulSession[S])` | Called instead of OnConnect when a session is restored from the SessionStore |
| `OnPanic` | `func(*StatefulSession[S], error)` | Called when a panic occurs during Handle or Update. When nil (default), the session is destroyed to prevent corrupted state. When set, the session survives and the developer assumes responsibility |
| `OnCommandDropped` | `func(*StatefulSession[S])` | Called when a command is dropped because buffers are full. When nil (default), the session is destroyed to prevent silent drift. When set, the developer handles it |
| `Freeze` | `FreezeMode` | Frozen mode for disconnected sessions. `FreezeWithRestore` requires OnRestore; `FreezeWithConnect` falls back to OnConnect. Zero disables. See [frozen mode](frozen-mode.md) |
| `Protocol` | `protocol.Protocol` | HTTP protocol (default `protocol.Auto` - detects per request). See [transport](transport.md#protocol-awareness) |
| `WireFormat` | `wire.Format` | Encoding for server-to-client updates (default `wire.JSON`) |

### Security

`App.Security` groups CSRF protection and session binding:

| Field | Type | Description |
|-------|------|-------------|
| `TrustedOrigins` | `[]string` | Origins allowed to make state-changing requests |
| `DisableSessionBinding` | `bool` | Disable User-Agent verification entirely (default: enabled) |
| `SessionMatch` | `func(original, reconnect string) bool` | Custom UA comparison. When nil, exact match. See [session binding](security.md#session-binding) |

---

## Session

`*tether.StatefulSession[S]` is the handle for an active connection. Methods are safe to call from any goroutine.

### State

```go
s.State()                       // read current state
s.Update(func(s State) State {  // mutate state from outside Handle
    s.Count++
    return s
})
s.ID()                          // unique session identifier
s.Context()                     // cancelled on permanent destruction (session lifetime)
s.Go(func(ctx context.Context) { ... }) // goroutine bound to transport lifetime (stops on disconnect)
s.Close()                       // close the transport connection
```

### Side effects

Call these inside `Handle` to buffer effects into the same update message, or from any goroutine for standalone updates:

```go
s.Toast("Settings saved")              // global notification
s.Announce("Item added to cart")       // screen reader live region
s.Flash("#notice", "Saved")            // notification at selector (5s)
s.ScrollTo("#new-item")               // smooth scroll element into view
s.Download("/export/report.csv")      // trigger file download via HTTP
s.Navigate("/success")                 // pushState
s.ReplaceURL("/current?saved=1")       // replaceState
s.SetTitle("Settings - My App")        // document.title
s.Signal("count", 42)                  // push reactive value
s.Signals(map[string]any{"a": 1})      // push multiple values
s.Push(push.Notification{...})         // Web Push notification
```

### Session (interface)

`tether.Session` is a non-generic interface exposing StatefulSession's side-effect methods without the state type parameter. It is available in `Handle`, `OnNavigate`, stateless page handlers, and reusable components.

Because Session has no generic parameter, component handlers can accept it directly - they don't need to know the application's state type:

```go
func todoHandle(sess tether.Session, ts TodoState, ev tether.Event) TodoState {
    sess.Toast("Saved")   // works - no generic needed
    sess.Signal("count", len(ts.Items))
    return ts
}
```

Methods: `ID`, `Context`, `Go`, `Toast`, `Navigate`, `ReplaceURL`, `SetTitle`, `Announce`, `Flash`, `ScrollTo`, `Download`, `Signal`, `Signals`, `Push`, `Close`.

`ID` returns an empty string in stateless page mode (StatelessConfig) - there is no persistent session. `Push` returns an error during pre-warming (initial GET) since no browser subscription exists yet. `Close` terminates the session's transport; in stateless page mode and tethertest it is a no-op. During stateful sessions all methods work normally.

---

## Event

```go
type Event struct {
    Type    event.Type        // Click, Input, Submit, Change, KeyDown, etc.
    Action  string            // application-defined action name
    Data    map[string]string // event-specific key-value pairs
    EventID string            // monotonic counter for correlation
    Target  string            // set by StatefulConfig.Components to the mount prefix
}

// Accessors
func (e Event) Value() string                      // e.Data["value"]
func (e Event) Key() string                        // keydown key name
func (e Event) Get(key string) (string, bool)      // check and get
func (e Event) Int(key string) (int, error)        // parse integer
func (e Event) Float64(key string) (float64, error) // parse float
func (e Event) Bool(key string) bool               // "true" → true
func (e Event) Bind(dest any) error                // struct-tag form binding
func (e Event) WithAction(action string) Event     // copy with different Action
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
consistency - one data extraction pattern across the framework.

**Single-value helpers** (return error on missing/invalid):

```go
p.Get("q")              // string - first value for key
p.Int("page")           // (int, error)
p.Float64("min")        // (float64, error)
p.Bool("active")        // true only for "true"
```

**Soft getters** (return default on missing/invalid - ideal for optional
URL parameters):

```go
p.IntDefault("page", 1)      // int - returns 1 if missing or invalid
p.Float64Default("min", 0.0) // float64
p.BoolDefault("drafts", false) // bool - returns default if key absent
```

**Multi-value helpers** (for repeated keys like `?tag=go&tag=web`):

```go
p.Strings("tag")        // []string - all values for key
p.Ints("id")            // ([]int, error)
p.Float64s("v")         // ([]float64, error)
```

---

## Handler

`*tether.Handler[S]` implements `http.Handler`.

```go
h := tether.Stateful(app, cfg)
mux.Handle("/app", h)

h.Health()          // HealthStatus{Pending, Active, Disconnected}
h.Drain(ctx)        // stop new sessions, wait for existing
h.Shutdown(ctx)     // close all sessions
```

### ListenAndServe

For single-handler apps, `Handler.ListenAndServe` handles signal trapping, graceful shutdown, and sensible defaults:

```go
h.ListenAndServe("")                      // checks PORT env, defaults to :8080
h.ListenAndServe(":3000")                 // explicit address
h.ListenAndServe("", existingMux)         // mount on an existing mux
h.ListenAndServeTLS("", "cert.pem", "key.pem")  // HTTPS, defaults to :443
```

On `SIGINT` or `SIGTERM`, sessions are drained gracefully (up to `Timeouts.ShutdownGrace`, default 10s) before the process exits.

For multi-handler apps, use the package-level `tether.ListenAndServe` which drains and shuts down all handlers concurrently:

```go
tether.ListenAndServe("", mux, wsHandler, sseHandler, swHandler)
```

Any `*Handler[S]` satisfies the `Drainable` interface automatically - no type-assert or adapter needed.

---

## Group

Broadcast state changes to multiple sessions:

```go
group := tether.NewGroup[State]()
group.Add(sess)
group.Remove(sess)
group.Len()       // member count (point-in-time)
group.Count()     // *Value[int] - reactive member count
group.All()       // iter.Seq[*StatefulSession[S]]

group.Broadcast(func(target *tether.StatefulSession[State], s State) State {
    s.Message = "hello"
    return s
})

group.BroadcastOthers(sender, func(target *tether.StatefulSession[State], s State) State {
    s.Message = "hello"
    return s
})
```

Optional callbacks: `group.OnJoin`, `group.OnLeave`.

For auto-registration, pass groups via `StatefulConfig.Groups`.

---

## Router

Dispatch `Render` and `Handle` by URL path:

```go
r := router.New[State](func(s State) string { return s.Page })
r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
r.Route("/settings", router.Page[State]{Render: settingsRender})
r.NotFound(router.Page[State]{Render: notFoundRender})

tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
})
```

---

## Assets

Two modes of operation:

**Embedded** (single binary, immutable):
```go
//go:embed static
var staticFS embed.FS

var assets = &tether.Asset{
    FS:     staticFS,
    Prefix: "/static/",
}
```

**Filesystem** (external, watched for changes):
```go
var assets = &tether.Asset{
    FS:       os.DirFS("./static"),
    Prefix:   "/static/",
    WatchDir: "./static",
}
```

When `WatchDir` is set, the asset manager uses `fsnotify` to watch the
directory. When a file changes, only that file's hash is recomputed.
Subsequent requests get the new hash in the URL, so browsers fetch the
updated asset. Call `assets.Close()` on shutdown to stop the watcher.

| Field | Type | Description |
|-------|------|-------------|
| `FS` | `fs.FS` | Asset filesystem. `embed.FS` or `os.DirFS`. Required |
| `Prefix` | `string` | URL path prefix (must end with `/`). Default `/assets/` |
| `WatchDir` | `string` | Filesystem path to watch. Empty disables watching |
| `Precache` | `[]string` | Asset paths for service worker pre-caching |

---

## Bind helpers

All helpers live in the `bind` package. Use `bind.Apply` to attach behaviours to any Fluent element:

```go
bind.Apply(btn,
    bind.OnClick("delete"),
    bind.Confirm("Sure?"),
    bind.Disable("Deleting..."),
)
```

### Server events

```go
bind.OnClick("act")         bind.OnSubmit("act")
bind.OnInput("act")         bind.OnChange("act")
bind.OnKeyDown("act")       bind.OnFocus("act")
bind.OnBlur("act")          bind.OnViewport("act")
bind.OnPaste("act")
bind.Event("dblclick", "act")
bind.Collect("#selector")
```

### Timing

```go
bind.Debounce(150*time.Millisecond)
bind.Throttle(time.Second)
bind.FilterKey("Enter")
bind.EventData("key", "val")
```

### Control

```go
bind.Disable("...")     bind.Confirm("...")
bind.Reset()            bind.AutoFocus()
bind.Indicator("#el")   bind.FocusTrap()
bind.PreventDefault()
bind.Required("msg")    bind.MinLength(8, "msg")
bind.MaxLength(140, "msg") bind.Pattern("regex", "msg")
```

### Signal bindings

```go
bind.BindText("count")           bind.BindShow("isOpen")
bind.BindHide("isOpen")          bind.BindClass("active", "sel")
bind.BindAttr("disabled", "busy") bind.BindValue("email")
```

### Client directives

```go
bind.Link()             bind.Cloak()
bind.Permanent()        bind.ToggleClass("cls")
bind.ToggleTarget("#x") bind.ToggleAttr("hidden")
bind.CopyToClipboard("#selector")
bind.ScrollTo("#selector")
bind.PreserveScroll()
bind.Editable("action")
bind.Selectable()
bind.CollectSelected("#list")
bind.OnSwipe("action")
bind.OnLongPress("action")
```

### Signal directives

```go
bind.ToggleSignal("menuOpen")
bind.SetSignal("tab", "settings")
bind.Optimistic("liked", "true")
bind.OptimisticToggle("liked")
```

### Uploads

```go
bind.Upload("avatar")
bind.UploadInput("#avatar-input")
bind.UploadProgress("avatar")
bind.PushSubscribe()
```

### Keyboard shortcuts

```go
bind.Hotkey("ctrl+k", "search.open")
bind.Hotkey("escape", "modal.close")
```

Global hotkeys that fire regardless of which element has focus. The combo
format uses `+` between modifiers and the key name. Multiple hotkeys can
be applied to the same element.

### Drag and drop

```go
bind.Draggable()             // mark as draggable
bind.DropTarget("card.move") // mark drop zone
bind.Sortable("card.reorder") // reorderable container
```

`Draggable` marks an element for dragging. Pair with `bind.EventData` to
carry identifying data. `DropTarget` fires the action on drop with merged
data from both the source and target. `Sortable` is like DropTarget but
also calculates the drop index for within-container reordering.

The `tether-drag-and-drop.js` extension is auto-included when any element
renders `data-tether-draggable` or `data-tether-sortable`.

### Lifecycle

```go
bind.Hook("chart")      bind.Transition("fade")
```

### Custom attributes

```go
bind.Data("tether-custom", "value")  // data-tether-custom="value"
```

---

## Middleware

Wraps `Handle` for cross-cutting concerns. Applied outermost-first:

```go
type Middleware[S any] func(HandleFunc[S]) HandleFunc[S]

tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
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
    Components: []tether.ComponentMount[State]{
        tether.Mount("likes", getLikes, setLikes),
    },
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
h.Replaced()                     // last URL used ReplaceURL (not Navigate)
```

### Middleware

`tethertest.Middleware` wraps the `Session`-based handler used by tethertest and `StatelessConfig`:

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

An optional `BusConfig` can customise async subscriber behaviour:

```go
var events = tether.NewBus[Event](tether.BusConfig{
    AsyncWorkers:  128,           // default 64
    AsyncOverflow: tether.Drop,   // default Block
})
```

| `BusConfig` field | Type | Default | Description |
|-------------------|------|---------|-------------|
| `AsyncWorkers` | `int` | 64 | Maximum concurrent goroutines for async subscribers |
| `AsyncOverflow` | `AsyncOverflow` | `Block` | What happens when all worker slots are full |

`AsyncOverflow` constants:

| Value | Behaviour |
|-------|-----------|
| `Block` | Wait for a slot. No data loss, but the publisher stalls |
| `Drop` | Discard the event and log a warning. Publisher never stalls |
| `Inline` | Run the callback synchronously in the publisher's goroutine |

### Publishing

```go
bus.Publish(msg)         // to all subscribers - use for external sources (DB, queues, cron)
bus.Emit(sess, msg)      // to all except sender - use inside Handle
bus.Len()                // active subscriber count
```

`Emit` accepts any `Session` value, so it can be called directly from `Handle` without a type-assert. In live sessions, publication is enqueued on the sender's command loop so the sender's diff is sent to the client before other subscribers react. Subscriptions registered via `tether.On` whose session ID matches the emitting session are automatically skipped - preventing double-apply since `Handle` already updated the sender's state.

### Subscribing

Raw subscription for non-session consumers (external services, monitoring):

```go
// Synchronous - callback runs in the publisher's goroutine. Must not block.
cancel := bus.Subscribe(ctx, func(msg ChatMessage) { ... })

// Asynchronous - callback runs in its own goroutine per event. Safe for I/O.
cancel := bus.SubscribeAsync(ctx, func(msg ChatMessage) { ... })
```

`Subscribe` runs the callback synchronously in the publisher's goroutine - it must not block. `SubscribeAsync` dispatches each event to a goroutine bounded by a semaphore (default 64 workers, configurable via `BusConfig.AsyncWorkers`). When all slots are full, the `BusConfig.AsyncOverflow` strategy applies: `Block` (wait), `Drop` (discard), or `Inline` (run synchronously). Use `SubscribeAsync` for external consumers that perform database writes, HTTP calls, or other I/O.

Session-aware subscription via `tether.On` - the primary way to connect a Bus to a session:

```go
tether.On(sess, messages, func(msg MessageSent, state ChatState) ChatState {
    state.Messages = append(state.Messages, msg.Text)
    return state
})
```

`tether.On` subscribes the session to the bus. When an event arrives, the callback runs inside the session's command loop (via `Session.Update`) with the event and the current state. The callback returns the new state - same pattern as Update.

Key behaviours:
- **Sender filtering** - if the event was emitted by this session (via `Bus.Emit`), the callback is skipped automatically
- **Auto-cleanup** - the subscription is removed when the session is destroyed (context cancelled)
- **Thread-safe** - the callback runs on the session's command loop, never concurrently with Handle or other Updates

Preferred usage is via `StatefulConfig.Watchers` for declarative subscription:

```go
Watchers: []tether.Watcher[State]{
    tether.WatchBus(activityBus, func(item ActivityItem, s State) State {
        s.Activity = append(s.Activity, item)
        return s
    }),
},
```

`tether.On` is still available for conditional subscriptions in `OnConnect`.

### Bus vs Group

Bus is parameterised on the **event type** - any session can subscribe regardless of its state type. Group requires all sessions to share the same state type. Use Bus for cross-handler communication; use Group for same-handler broadcasting.

---

## Value

Shared observable state that notifies sessions when it changes. Built on top of Bus internally:

```go
var onlineCount = tether.NewValue(0)
```

### Reading and writing

```go
v.Load()              // lock-free read - safe from any goroutine
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
- **Immediate sync** - the callback fires once with the current value at subscription time
- **Atomic subscribe+read+apply** - the subscription, read, and initial state application happen within a single session command, so a concurrent Store is always ordered after the initial value
- **Auto-cleanup** - removed when the session is destroyed
- **Thread-safe** - runs on the session's command loop

Preferred usage is via `StatefulConfig.Watchers` for declarative subscription:

```go
Watchers: []tether.Watcher[State]{
    tether.WatchValue(onlineCount, func(count int, s State) State {
        s.OnlineCount = count
        return s
    }),
},
```

`tether.Observe` is still available for conditional subscriptions in `OnConnect`.

### Value vs Bus

Use Value for state that multiple sessions need to stay in sync with (online counts, shared configuration, room membership). Use Bus for discrete domain events (chat messages, notifications, activity feeds).

---

## Component

A self-contained rendering unit with its own state. Components know how to render themselves and handle their own events, without any knowledge of the parent's state type:

```go
type Component interface {
    Render() node.Node
    Handle(Session, Event) Component
}
```

Components are value types - `Handle` returns a new value, the receiver is never mutated. This matches the `HandleFunc` pattern (returns new S).

### EqualComponent

Optional interface for fast equality checking. Implement this when your component contains slices or maps that make `reflect.DeepEqual` expensive:

```go
type EqualComponent interface {
    Component
    EqualComponent(Component) bool
}
```

### Route and RouteTyped

Dispatch events to a component by prefix. Events with actions starting with `"prefix."` are routed to the component with the prefix stripped:

```go
// Route returns Component - use when the field stores the interface type.
s.Chat = tether.Route(s.Chat, "chat", sess, ev)

// RouteTyped preserves the concrete type - use when the field stores a concrete struct.
s.Chat = tether.RouteTyped(s.Chat, "chat", sess, ev)
```

`RouteTyped` is the common choice. It preserves compile-time type safety - the parent stores the concrete component type in its state struct with direct field access, no type assertions needed.

### StatefulConfig.Components

Declarative component mounting. The framework intercepts events matching each mount's prefix and dispatches them to the component's `Handle` - the page's `Handle` function never sees these events:

```go
tether.StatefulConfig[State]{
    Components: []tether.ComponentMount[State]{
        tether.Mount("likes",
            func(s State) counter.Counter { return s.Likes },
            func(s State, c counter.Counter) State { s.Likes = c; return s },
        ),
        tether.Mount("stars",
            func(s State) counter.Counter { return s.Stars },
            func(s State, c counter.Counter) State { s.Stars = c; return s },
        ),
    },
}
```

`Mount` follows the same pattern as `WatchValue` and `WatchBus`: a generic constructor that returns a non-generic interface, so `StatefulConfig.Components` can hold mounts for different component types.

Navigate events bypass mounts - they always reach `OnNavigate`.

### Event.Target

When `StatefulConfig.Components` dispatches an event, the framework sets `Event.Target` to the mount's prefix (e.g. `"likes"` or `"stars"`). Middleware and logging can inspect this field to identify which component handled the event without parsing the action string.

### Event.WithAction

Returns a copy of the event with a different `Action`. Used by `Route`, `RouteTyped`, and the mount system to strip prefixes before forwarding to the component.

### Mounter

Optional interface for one-time component setup. The framework calls `Mount` once per component during session startup (after the command loop starts, before any client events arrive) for components registered via `StatefulConfig.Components`:

```go
type Mounter interface {
    Component
    Mount(Session) Component
}
```

Use Mount for initial side effects - `sess.Toast("Ready")`, `sess.Signal(...)`, `sess.Go(...)` - that a component needs when it first appears. Components that don't need setup simply implement `Component` without `Mounter`.

### Route vs StatefulConfig.Components

Use `StatefulConfig.Components` when the component is self-contained and the page's `Handle` never needs to see its events. Use `Route`/`RouteTyped` in Handle when you need to coordinate component events with other state changes.

### RouteMount

Exported function used by the exec loop and `tethertest` to dispatch component events:

```go
func RouteMount[S any](mounts []ComponentMount[S], sess Session, state S, ev Event) (S, bool)
```

Returns the updated state and `true` if a mount handled the event, or the original state and `false` if no prefix matched.

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

**Sentinel errors** - check with `errors.Is()`:

| Error | Meaning |
|-------|---------|
| `tether.ErrPushNotConfigured` | Handler created without `PushConfig` |
| `tether.ErrPushNoSubscription` | Browser has not registered a push subscription |
| `tether.ErrPushPreWarm` | Push called during pre-warming (no browser yet) |
| `push.ErrSubscriptionExpired` | Push service returned HTTP 410 |

---

## Window

Virtual scrolling helper for large lists. See the [windowing guide](windowing.md) for full usage.

```go
import "github.com/jpl-au/tether/window"

window.New(window.Config{
    Total:     len(s.Items),
    Offset:    s.ScrollOffset,
    PageSize:  30,
    RowHeight: 40,
    Row:       func(i int) node.Node { return renderRow(s.Items[i]) },
})
```

| `Config` field | Type | Description |
|----------------|------|-------------|
| `Total` | `int` | Full dataset size |
| `Offset` | `int` | First visible item index |
| `PageSize` | `int` | Items to render (viewport + buffer) |
| `RowHeight` | `int` | Uniform row height in pixels |
| `Row` | `func(int) node.Node` | Renders a single item by index |

---

[← Back to documentation](../README.md#documentation)
