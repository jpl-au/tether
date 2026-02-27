# AGENTS.md

## Project overview

fluent-poly is a reactive server-driven UI layer for Go. It connects [Fluent](https://github.com/jpl-au/fluent) node trees to the browser, sending only the parts that changed as targeted patches. The client morphs the DOM in place using idiomorph.

The architecture has three layers: Fluent builds HTML node trees, fluent-jit diffs them, and fluent-poly manages sessions, transports, and the client runtime.

## Build and test

Run all three before submitting changes:

```bash
go build ./...
go test ./...
go vet ./...
```

Local development uses `replace` directives in `go.mod` pointing to sibling directories (`../fluent`, `../fluent-jit`). These repos may be on feature branches with unpublished changes — do not attempt `go get @latest` for them.

## Package layout

```
poly/           Root — Transport, Session, Config, Event, Group, protocol
poly/bind/      Event bindings, signal bindings, client directives, upload helpers
poly/router/    URL router — dispatches Render/Handle by path
poly/ws/        WebSocket transport (only package importing coder/websocket)
poly/sse/       SSE+POST transport (no external dependencies)
poly/push/      Web Push notification sending (RFC 8291 + RFC 8292)
poly/polytest/  Test harness for Handle functions (no channels, no transports)
poly/client/    Embedded JS files (fluent-poly.js, idiomorph.min.js, poly-worker.js)
```

Source files in the root package, split by concern:

```
config.go       Config, TransportMode, PushConfig, defaults
handler.go      Package doc, Handler, New(), ServeHTTP, origin checking, POST handlers
lifecycle.go    serveInitialPage, serveSession, reattach, wireDisconnect
pending.go      reapPending — periodic cleanup for pre-warmed sessions
drain.go        Drain, Shutdown
health.go       HealthStatus, Health()
page.go         Initial page rendering — polyBody, newID
session.go      Session struct, ID(), Context(), Go(), constants
loop.go         Command loop — run(), readTransport(), exec(), onTransportClose(), cleanup()
methods.go      Dual-path methods — State(), Update(), Toast(), Navigate(), SetTitle(), etc.
handle.go       HandleFunc type definition
effects.go      Internal effects accumulator (replaces HandleResult)
group.go        Broadcasting — Group, Broadcast, BroadcastOthers, All()
protocol.go     Wire format types and encoding
transport.go    Transport interface
event.go        Event and Params types, convenience helpers (Key, Int, Bool, Get, Float64)
event_bind.go   Event.Bind — reflection-based form field decoding
middleware.go   Middleware type and chain function
catch.go        Catch — render-level error boundary with panic recovery
embed.go        Client JS embedding, ServeClient
push/push.go    Web Push protocol — Send(), GenerateVAPIDKeys(), VAPID auth, aes128gcm encryption
```

Transport implementations live in sub-packages. The `Config.Upgrade` field accepts any function that returns a `Transport`, keeping the root package transport-agnostic. `Config.Fallback` provides a secondary transport (typically SSE) used when the primary is unavailable.

## Event flow

1. Client JS sends a DOM event as JSON: `{"type":"click","action":"increment","data":{}}`
2. `Transport.ReceiveEvent()` deserialises it to an `Event`
3. The session's command loop calls the user's `Handle` function with the session and current state; Handle returns the new state directly and may call session methods (`Toast`, `Announce`, `Flash`, `Navigate`, `SetTitle`) to buffer side effects
4. The returned state is rendered to a new node tree and diffed against the previous render; buffered effects are merged into the update
5. `Transport.SendUpdate()` sends a unified update message containing either:
   - **Patches** — targeted content updates for keyed elements that changed
   - **Morphs** — structural DOM changes (e.g. root morph when keys are added/removed/reordered)
6. Client JS applies patches first, then morphs

### Panic recovery

If `Handle` or `Render` panics during event processing, the panic is recovered, logged with the session ID and action, and the event is dropped. The session's command loop continues processing subsequent events. `Session.Update` has the same recovery — a panicking callback does not crash the caller's goroutine.

### Wire format

All updates use a single `"update"` message type:

```json
{"type":"update","patches":[{"key":"count","html":"<span>43</span>"}]}
{"type":"update","morphs":[{"key":"","html":"<div>...</div>"}]}
{"type":"update","title":"New Page"}
```

An empty `key` in morphs targets the root element. A non-empty key targets a specific keyed container (for scoped morphs). The `title` field updates `document.title`. The `url` and `replace` fields control browser URL changes.

### Structural change diagnostics

When a structural change is detected (keys added, removed, or reordered), the session logs a warning with actionable details:

```
WARN structural change, sending root morph session=abc change="key 'help' added" bytes=15234 tip="wrap conditional elements in a keyed container to scope this morph"
```

The `change` field reports exactly which keys were added, removed, or if they were reordered. The `tip` guides the developer toward wrapping conditional elements in a stable keyed container to avoid full-page morphs.

For production telemetry, use the `OnStructuralChange` callback on `Config`:

```go
poly.New(poly.Config[State]{
    OnStructuralChange: func(s *poly.Session[State], c poly.StructuralChange) {
        metrics.Counter("structural_changes").Inc()
        log.Printf("keys added=%v removed=%v reordered=%v bytes=%d",
            c.Added, c.Removed, c.Reordered, c.Bytes)
    },
    // ...
})
```

The callback runs inside the session's command loop, so keep it fast — offload expensive work to a goroutine. The `StructuralChange` struct has `Added`, `Removed` (key slices), `Reordered` (bool), and `Bytes` (re-rendered HTML size).

## Event binding

Event binding helpers live in the `bind` package. There are two equivalent ways to bind events to elements. Both produce the same `data-poly-*` attribute:

```go
// Helper (convenience — wraps SetData with the correct convention string)
bind.Click(button.Text("+"), "increment")

// Direct (explicit — useful when you need full control)
button.Text("+").SetData("poly-click", "increment")
```

The helpers (`Click`, `Submit`, `Input`, `Change`, `KeyDown`, `Focus`, `Blur`, `Viewport`) are defined in `bind/event.go`. They work with any Fluent element type via a generic `Settable` constraint.

### Keydown modifiers

Keydown events include modifier key state in `Event.Data`: `ctrl`, `shift`, `alt`, `meta` are set to `"true"` when held. This enables keyboard shortcut handling:

```go
func handle(_ *poly.Session[state], s state, ev poly.Event) state {
    if ev.Action == "shortcut" && ev.Data["ctrl"] == "true" && ev.Data["key"] == "s" {
        // Ctrl+S pressed
    }
    return s
}
```

### Timing control

Input events are debounced at `DefaultDebounce` (default 300ms). Override per element:

```go
bind.Debounce(bind.Input(input.Text("q", ""), "search"), 150*time.Millisecond)
```

Throttle any event type:

```go
bind.Throttle(bind.Click(button.Text("Fire"), "fire"), time.Second)
```

### Loading states

Elements with `data-poly-disable` are disabled while an event is in flight and restored when the next server update arrives:

```go
bind.Disable(bind.Click(button.Text("Save"), "save"), "Saving...")
```

If the text argument is non-empty, the element's text content is temporarily replaced.

### Confirmation dialogs

Elements with `data-poly-confirm` show `window.confirm` before the event is sent:

```go
bind.Confirm(bind.Click(button.Text("Delete"), "delete"), "Are you sure?")
```

If the user cancels, the event is dropped entirely.

### Focus management

Elements with `data-poly-autofocus` receive focus after patches and morphs are applied:

```go
bind.AutoFocus(input.Text("name", ""))
```

Focus is applied after patches and morphs. Only one element should have this attribute at a time.

### URL routing

Bidirectional sync between Go state and the browser URL. Anchors with `data-poly-link` are intercepted by the JS runtime — instead of a full page load, the URL is pushed via `history.pushState` and a navigate event is sent to the server.

```go
// Config — OnNavigate processes URL changes on initial load and navigation
OnNavigate: func(_ poly.PreSession, state State, params poly.Params) State {
    state.Page = params.Path
    return state
},

// Mark an anchor for client-side navigation
bind.Link(a.Link("/profile", "Profile"))

// Equivalent using SetData directly
a.Link("/profile", "Profile").SetData("poly-link", "")
```

`OnNavigate` is called:
1. On initial page load (after `InitialState`), with the request URL
2. On link clicks within `[data-poly-link]` anchors
3. On browser back/forward (popstate)

If `OnNavigate` is nil, navigation events fall through to `Handle` as `Event{Type: "navigate", Data: {"path": "...", "search": "..."}}`.

**Server-initiated URL and title updates:**

```go
session.Navigate("/success")               // pushState (adds history entry)
session.ReplaceURL("/current?saved=true")   // replaceState (no history entry)
session.SetTitle("Settings — My App")       // updates document.title
```

URL and title updates can accompany DOM patches/morphs in the same message, or be sent standalone.

**Wire format — navigate event from client:**

```json
{"type":"navigate","action":"","data":{"path":"/profile","search":"tab=settings"}}
```

**Wire format — URL/title command from server:**

```json
{"type":"update","url":"/profile","replace":false,"title":"Profile"}
```

### Client-side directives

Client-side toggles run entirely in the browser without a server round-trip. They are intended for ephemeral UI state like menus, modals, and accordions.

```go
// Toggle a CSS class on the element itself
bind.ToggleClass(button.Text("Menu"), "is-open")

// Toggle a CSS class on a different element
bind.ToggleClass(bind.ToggleTarget(button.Text("Menu"), "#nav"), "is-open")

// Toggle visibility via the hidden attribute
bind.ToggleAttr(bind.ToggleTarget(button.Text("Show Help"), "#help"), "hidden")
```

Helpers: `ToggleClass`, `ToggleTarget`, `ToggleAttr`. Data attributes: `data-poly-toggle-class`, `data-poly-toggle-target`, `data-poly-toggle-attr`.

Client-managed state survives server morphs automatically. If the element is removed entirely (not morphed), the client state is lost — this is by design.

**Performance:** The generic bind helpers are ~47% slower than raw `SetData` and add 2 extra allocations per element. For performance-sensitive code, prefer `SetData` directly. Run `go test -bench=BenchmarkBind -benchmem` in the `bind/` directory to compare.

**PGO:** Applications consuming fluent-poly benefit from [Profile-Guided Optimization](https://go.dev/doc/pgo). Collect a CPU profile from production and place it as `default.pgo` in the main package. Do not commit a `default.pgo` into this library — PGO profiles are application-specific.

## Event helpers

Typed accessors on `Event` for convenient data extraction:

```go
ev.Value()                    // shorthand for ev.Data["value"]
ev.Key()                      // keydown key name (ev.Data["key"])
name, ok := ev.Get("name")   // check and get value
count, err := ev.Int("count") // parse integer
ratio, err := ev.Float64("ratio") // parse float
if ev.Bool("confirmed") { ... }   // "true" → true

// Decode multiple form fields into a struct
var form struct {
    Email string `poly:"email"`
    Age   int    `poly:"age"`
}
if err := ev.Bind(&form); err != nil { ... }
```

`Bind` uses reflection with `poly` struct tags. Supports `string`, `int`, `int64`, `float64`, and `bool` fields. Untagged exported fields default to the lowercased field name.

## Middleware

`Middleware[S]` wraps the Handle function for cross-cutting concerns. Applied outermost-first — the first entry in the slice is the outermost layer:

```go
type Middleware[S any] func(HandleFunc[S]) HandleFunc[S]

func withLogging[S any](next poly.HandleFunc[S]) poly.HandleFunc[S] {
    return func(sess *poly.Session[S], s S, ev poly.Event) S {
        slog.Info("event", "action", ev.Action)
        return next(sess, s, ev)
    }
}

poly.New(poly.Config[State]{
    Middleware: []poly.Middleware[State]{withLogging, withAuth},
    // ...
})
```

Middleware is applied during `New()` by composing the chain around `Handle`.

## Error boundaries with Catch

`poly.Catch` wraps a render function with panic recovery, returning a fallback node if the component panics:

```go
func render(s State) node.Node {
    return div.New(
        header(s),
        poly.Catch(func() node.Node {
            return riskyWidget(s)
        }, span.Text("Widget unavailable")),
        footer(s),
    )
}
```

The rest of the page renders normally. Panics are logged via `slog.Error`.

## Apply composition helper

`bind.Apply` chains multiple behaviours on an element top-to-bottom instead of inside-out nesting:

```go
// Nested (inside-out)
bind.Disable(bind.Confirm(bind.Click(btn, "delete"), "Sure?"), "Deleting...")

// Applied (top-to-bottom)
bind.Apply(btn,
    bind.OnClick("delete"),
    bind.WithConfirm("Sure?"),
    bind.WithDisable("Deleting..."),
)
```

Option helpers: `OnClick`, `OnSubmit`, `OnInput`, `OnChange`, `OnKeyDown`, `OnFocus`, `OnBlur`, `OnViewport`, `WithDisable`, `WithConfirm`, `WithPreserve`, `WithAutoFocus`, `WithIndicator`, `WithFocusTrap`, `WithDebounce`, `WithThrottle`, `WithFilterKey`, `WithEventData`, `WithLink`, `WithToggleClass`, `WithToggleTarget`, `WithToggleAttr`, `WithCloak`, `WithPermanent`, `WithHook`, `WithTransition`, `WithBindText`, `WithBindShow`, `WithBindHide`, `WithBindClass`, `WithBindAttr`, `WithBindValue`, `WithToggleSignal`, `WithSetSignal`, `WithOptimistic`, `WithOptimisticToggle`, `WithUpload`, `WithUploadProgress`.

## Form validation

Validation is handled server-side in the `Handle` function — no special framework support is needed. The pattern is: add error fields to state, validate in the handler, and render error messages alongside the form.

```go
// State tracks both the current input value and any error message.
type state struct {
    TodoText  string // preserves input on validation failure
    TodoError string // error message shown below the form
    // ...
}

// Handle validates on submit and clears errors on success.
func handle(sess *poly.Session[state], s state, ev poly.Event) state {
    switch {
    case ev.Action == "add":
        text := strings.TrimSpace(ev.Data["text"])
        if text == "" {
            s.TodoError = "Please enter a todo."
            s.TodoText = ev.Data["text"]
            return s
        }
        s.TodoError = ""
        s.TodoText = ""
        // ... add the item
        sess.Announce("Todo added")
        return s

    case ev.Action == "validate-todo":
        // Live validation on input — clears error once corrected
        text := ev.Data["value"]
        if len(text) > 200 {
            s.TodoError = fmt.Sprintf("Too long — %d/200 characters.", len(text))
        } else if s.TodoError != "" {
            s.TodoError = ""
        }
        s.TodoText = text
    }
    return s
}
```

**Key patterns:**

- **Wrap form + error in a Dynamic key** so the server controls field values via targeted patches:
  ```go
  div.New(
      bind.Preserve(form.New(/*...*/)),
      span.Text(s.TodoError).Style("color: #c33"),
  ).Dynamic("todo-form")
  ```

- **`bind.Preserve`** prevents the JS runtime from resetting form fields after submit. Without it, `target.reset()` clears the user's input before the server morph arrives — losing the text on validation failure.

- **Live validation via `bind.Input`** with a dedicated action (e.g. `"validate-todo"`) gives the user feedback as they type, debounced at 300ms.

- **Keep error spans always in the tree** (empty text when no error) to avoid structural changes that trigger root morphs. Toggling between "error present" and "no error" should be a content patch, not a structural one.

## JS hooks

Elements with `data-poly-hook` receive JavaScript lifecycle callbacks, enabling integration with third-party libraries (charts, maps, rich text editors):

```go
bind.Hook(div.New(), "chart")
```

```js
Poly.hooks.chart = {
    mounted: function(el) { /* initialise — el is in the DOM */ },
    updated: function(el) { /* re-render — el was morphed */ },
    destroyed: function(el) { /* teardown — el is about to be removed */ }
};
```

All three callbacks are optional.

**Lifecycle timing:**
- `mounted` fires in `afterNodeAdded` — the element is in the DOM
- `updated` fires in `afterNodeMorphed` — the element was morphed in place
- `destroyed` fires in `beforeNodeRemoved` — the element is still in the DOM but about to be removed. For elements with transitions, destroyed fires before the leave transition starts.

## Transitions

Elements can opt into CSS transitions during morph-driven add/remove by setting `data-poly-transition` (or using the `bind.Transition` helper):

```go
bind.Transition(div.New(children...), "fade")
// produces: data-poly-transition="fade"
```

The JS runtime applies CSS classes following a naming convention — the developer writes standard CSS to control timing and effect:

```css
.item { opacity: 1; transition: opacity 0.3s; }
.poly-fade-enter { opacity: 0; }
.poly-fade-leave { opacity: 0; }
```

**Enter lifecycle:** When Idiomorph adds a node with `data-poly-transition="fade"`, the runtime adds `poly-fade-enter` before insertion, forces a reflow, then removes the class — triggering the CSS transition from the enter state to the element's default state.

**Leave lifecycle:** When Idiomorph wants to remove a node with `data-poly-transition="fade"`, the runtime prevents immediate removal, adds `poly-fade-leave`, and waits for `transitionend` before removing the node from the DOM.

**Fallback timeout:** If `transitionend` never fires (no CSS transition defined, `display: none`, etc.), the node is removed after `TransitionTimeout` (default 5 seconds) rather than leaking.

**Leave cancellation:** If a morph arrives that includes an element currently playing a leave transition, the leave is cancelled and the element is morphed normally. This handles rapid state changes (e.g. user toggles something twice quickly).

## Broadcasting

`Group[S]` tracks a set of sessions for multi-client updates:

```go
group := poly.NewGroup[State]()

poly.New(poly.Config[State]{
    OnConnect:    func(s *poly.Session[State]) { group.Add(s) },
    OnDisconnect: func(s *poly.Session[State]) { group.Remove(s) },
    // ...
})

group.Broadcast(func(target *poly.Session[State], s State) State {
    s.Notification = "System update"
    return s
})
```

`Broadcast` queues a state update on each session's command channel. The callback receives the target session and its current state — return the new state. Each session processes the update in its own command loop (no goroutine-per-session). `Add`, `Remove`, `Broadcast`, `BroadcastOthers`, `Len`, and `All` are all safe to call from any goroutine.

`BroadcastOthers` excludes a session from the broadcast. This is the typical pattern when broadcasting from inside Handle — the sender's state is already updated via the return value, so BroadcastOthers pushes the change to everyone else without double-applying to the sender:

```go
Handle: func(sess *poly.Session[State], s State, ev poly.Event) State {
    if ev.Action == "send-message" {
        s.Messages = append(s.Messages, ev.Data["text"])
        group.BroadcastOthers(sess, func(target *poly.Session[State], s State) State {
            s.Messages = append(s.Messages, ev.Data["text"])
            return s
        })
    }
    return s
},
```

### Presence

`Group` has optional `OnJoin` and `OnLeave` callbacks that fire when sessions are added or removed. Both run outside the group lock so it is safe to call `Broadcast` or other Group methods from within.

- `OnJoin` fires only for new sessions (duplicate `Add` is a no-op).
- `OnLeave` fires only for sessions that were in the group (absent `Remove` is a no-op).
- `All()` returns an iterator over all sessions — use `sess.State()` to read each session's data (e.g. usernames for an online list).

## Session context and background goroutines

Each session carries a `context.Context` that is cancelled when the session is permanently destroyed (reaped from the disconnected pool, or shutdown). The context survives temporary disconnects and reconnects.

```go
OnConnect: func(s *poly.Session[State]) {
    // Launch a ticker bound to the session's lifetime.
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

`Session.Go(fn)` launches a goroutine with the session's context. When the session is reaped or the handler shuts down, the context is cancelled and the goroutine exits cleanly. No global maps, no done channels, no OnDisconnect cleanup.

`Session.Context()` returns the context directly for use cases where Go() is not needed (e.g. passing to database queries or HTTP clients).

**Cancellation points:**
- Per-session disconnect timer — reconnect window expired
- Per-session idle timer — no activity within `IdleTimeout`
- Per-session max lifetime timer — session exceeded `MaxLifetime`
- `wireDisconnect` when `ReconnectTimeout <= 0` — reconnection disabled, session gone on first disconnect
- `Shutdown` — all sessions destroyed

## Concurrency model

The codebase follows a three-tier pattern for synchronisation:

| Tier | Mechanism | Use case | Examples |
|------|-----------|----------|----------|
| **Channels** | Buffered command channels | State mutations, sequential processing | Session command loop, SSE writer goroutine |
| **Atomics** | `atomic.Value` with copy-on-write | Hot-path reads, lock-free dispatch | `Bus` subscribers, `Group` sessions, `Router` pages, `Value` store |
| **Mutexes** | `sync.Mutex` on write path only | Infrequent registry writes | `Group.wmu`, `Router.wmu`, `Bus.wmu` |

The copy-on-write pattern: the write path copies the entire map under a mutex, modifies the copy, and stores it atomically. The read path does a single `atomic.Value.Load()` with no locking. This is ideal for registries with many concurrent readers and infrequent writes.

## Reactive signals

Alongside the core server-rendering model, fluent-poly supports a signal-based reactive layer for lightweight targeted updates. Signals let the server push individual values to the client without a full render cycle — bound elements update instantly with no diff.

```go
sess.Signal("count", 42)                          // push a single value
sess.Signals(map[string]any{"count": 42})          // push multiple values
```

Signal bindings in `bind/signal.go` (`BindText`, `BindShow`, `BindHide`, `BindClass`, `BindAttr`, `BindValue`) work document-wide — not just inside the poly root. This means navigation highlights, status indicators, and layout shell elements react instantly.

Client-side signal directives (`bind.ToggleSignal`, `bind.SetSignal`) update signals in the browser without contacting the server. Optimistic updates (`bind.Optimistic`, `bind.OptimisticToggle`) set a signal immediately before the event reaches the server — the server can correct the value in its response.

See [signals docs](docs/signals.md) for the full guide. For the complete public API surface, see [API reference](docs/api.md).

## Transport mode

The `Mode` field on `Config` controls which transports the handler accepts. It is an enum (`TransportMode`) with three values:

| Mode | Constant | Behaviour |
|------|----------|-----------|
| WebSocket only | `poly.WebSocketOnly` | Default. Only WebSocket upgrades are accepted. `Fallback` is ignored. |
| SSE only | `poly.SSEOnly` | Only SSE+POST. `Upgrade` is ignored; `Fallback` must be set. |
| Auto fallback | `poly.WebSocketWithFallback` | Tries WebSocket first; falls back to SSE+POST if the initial connection fails. Both `Upgrade` and `Fallback` must be set. |

```go
// WebSocket only (default — Mode can be omitted)
poly.New(poly.Config[State]{
    Upgrade: ws.Upgrade(),
    // ...
})

// SSE only
poly.New(poly.Config[State]{
    Mode:     poly.SSEOnly,
    Fallback: sse.Upgrade(),
    // ...
})

// WebSocket with SSE fallback
poly.New(poly.Config[State]{
    Mode:     poly.WebSocketWithFallback,
    Upgrade:  ws.Upgrade(),
    Fallback: sse.Upgrade(),
    // ...
})
```

### How it works

The initial HTML includes a `data-poly-transport` attribute on the root element with the value `"ws"`, `"sse"`, or `"auto"`. The client JS reads this to determine the connection strategy:

- **`ws`** — connects via WebSocket only; reconnects via WebSocket on disconnect.
- **`sse`** — connects via SSE+POST only; never attempts WebSocket.
- **`auto`** — tries WebSocket first. If the first attempt fails, switches to SSE+POST permanently for that page load.

When SSE is active, client events are sent as HTTP POST requests to the same endpoint.

**SSE heartbeat:** Set `HeartbeatInterval` (default 20s) to send keep-alive comments that prevent proxies from closing idle SSE connections. Set to `-1` to disable. WebSocket transports have their own ping/pong and do not need this.

**SSE reconnection:** Reconnection is automatic. When an SSE connection drops, the session is preserved. On reconnect, a full re-render is sent to bring the client up to date.

**Wire format:** Identical JSON regardless of transport.

## Complete helper reference

All helpers are in the `bind` package. They accept any Fluent element type via a generic `Settable` constraint.

**Server events** (`bind/event.go`):

| Helper | Data attribute | Purpose |
|--------|---------------|---------|
| `bind.Click` | `poly-click` | Server event on click |
| `bind.Submit` | `poly-submit` | Server event on form submit |
| `bind.Input` | `poly-input` | Server event on input (debounced) |
| `bind.Change` | `poly-change` | Server event on value commit |
| `bind.KeyDown` | `poly-keydown` | Server event on keydown (with modifiers) |
| `bind.FilterKey` | `poly-filter-key` | Only fire keydown for a specific key |
| `bind.Focus` | `poly-focus` | Server event on focus |
| `bind.Blur` | `poly-blur` | Server event on blur |
| `bind.Viewport` | `poly-viewport` | Server event on viewport enter/exit |
| `bind.EventData` | `poly-data-{key}` | Attach extra key-value data to events |
| `bind.Debounce` | `poly-debounce` | Override input debounce delay |
| `bind.Throttle` | `poly-throttle` | Minimum interval between events |

**Signal bindings** (`bind/signal.go`):

| Helper | Data attribute | Purpose |
|--------|---------------|---------|
| `bind.BindText` | `poly-bind-text` | Set textContent from signal |
| `bind.BindShow` | `poly-bind-show` | Show element when signal is truthy |
| `bind.BindHide` | `poly-bind-hide` | Hide element when signal is falsy |
| `bind.BindClass` | `poly-bind-class` | Toggle CSS class from signal |
| `bind.BindAttr` | `poly-bind-attr` | Set/remove attribute from signal |
| `bind.BindValue` | `poly-bind-value` | Set form field value from signal |

**Control directives** (`bind/control.go`):

| Helper | Data attribute | Purpose |
|--------|---------------|---------|
| `bind.Disable` | `poly-disable` | Disable element while event in flight |
| `bind.Confirm` | `poly-confirm` | Show confirmation before sending event |
| `bind.Preserve` | `poly-preserve` | Prevent form reset after submit |
| `bind.AutoFocus` | `poly-autofocus` | Focus element after server update |
| `bind.Indicator` | `poly-indicator` | Show loading indicator at selector |
| `bind.FocusTrap` | `poly-focus-trap` | Trap keyboard focus within element |

**Client-side directives** (`bind/directive.go`):

| Helper | Data attribute | Purpose |
|--------|---------------|---------|
| `bind.Link` | `poly-link` | Client-side navigation |
| `bind.ToggleClass` | `poly-toggle-class` | Client-side CSS class toggle |
| `bind.ToggleTarget` | `poly-toggle-target` | Direct toggle at different element |
| `bind.ToggleAttr` | `poly-toggle-attr` | Client-side boolean attribute toggle |
| `bind.Cloak` | `poly-cloak` | Hide until runtime initialises |
| `bind.Permanent` | `poly-permanent` | Exclude element from morphing |
| `bind.ToggleSignal` | `poly-toggle-signal` | Toggle boolean signal on click |
| `bind.SetSignal` | `poly-set-signal` | Set signal to value on click |
| `bind.Optimistic` | `poly-optimistic` | Set signal before server round-trip |
| `bind.OptimisticToggle` | `poly-optimistic-toggle` | Toggle signal before server round-trip |

**File uploads** (`bind/upload.go`):

| Helper | Data attribute | Purpose |
|--------|---------------|---------|
| `bind.Upload` | `poly-upload` | Trigger file upload on click/change |
| `bind.UploadProgress` | `poly-bind-attr` | Bind progress value to upload signal |

**Lifecycle hooks** (`bind/interop.go`):

| Helper | Data attribute | Purpose |
|--------|---------------|---------|
| `bind.Hook` | `poly-hook` | JS lifecycle callbacks |
| `bind.Transition` | `poly-transition` | CSS enter/leave transitions |

## Code style

- Comments explain why, not what
- Names must not repeat their package context (`bind.Click` not `bind.BindClick`)
- Receiver names: one or two letters (`s` for Session, `h` for handler, `t` for transport)
- Variable name length proportional to scope size
- No `Get` prefix on getters (`ID()` not `GetID()`)
- Exported symbols at the top of each file, unexported below
- Test failures must include expected and actual values: `t.Errorf("expected %d, got %d", want, got)`
- Avoid verbose function/method names — this is Go, not Java

## Testing

Tests are split by concern into separate files:

```
mock_test.go        Shared test infrastructure (mockTransport, counterState, helpers)
session_test.go     Core event loop (patch, morph, equality, multiple events, disconnect)
navigate_test.go    URL navigation
recover_test.go     Panic recovery
title_test.go       Page title updates
group_test.go       Broadcasting (uses testing/synctest)
context_test.go     Session context and Go lifecycle
activity_test.go    Server-initiated update refreshes lastActivity
reap_test.go        Per-session timer tests: idle, max lifetime, disconnect (uses testing/synctest)
shutdown_test.go    Graceful shutdown (uses testing/synctest)
drain_test.go       Graceful drain (rejects new, allows reconnect, context cancellation)
origin_test.go      Origin checking and CSRF protection
worker_test.go      Service worker header, polyBody attributes, push subscribe handler
bind_test.go        Event binding helpers (package poly_test, black-box)
protocol_test.go    Wire format encoding
bench_test.go       Performance benchmarks
announce_test.go    Live region announcements (Session.Announce, wire format)
presence_test.go    Group presence (OnJoin, OnLeave, All)
flash_test.go       Session.Flash one-time notifications
health_test.go      Handler.Health session pool counts
sse/sse_test.go             SSE transport
push/push_test.go           VAPID key generation, JWT signing, Send end-to-end
router/helpers_test.go      Shared test types and selector function
router/dispatch_test.go     Render/Handle dispatch (matching, not-found, nil)
router/route_test.go        Route registration, overwrite, concurrent access
```

`mock_test.go` defines `mockTransport` (replays queued events, records sent `Update` values) and `newTestSession` (creates a session with channels, a seeded differ, and a logger — ready for `go sess.readTransport(sess.events)` + `go sess.run()`). Helper functions `patchUpdates()` and `morphUpdates()` filter recorded updates by type. Use these for any new session behaviour tests.

Most tests that start a session loop use `testing/synctest.Test` for deterministic timing. The standard cleanup pattern is `defer func() { sess.stop(); synctest.Wait() }()` registered immediately after `go sess.run()` — this stops the command loop before the synctest bubble exits.

`bind_test.go` uses `package poly_test` (black-box) because it verifies the public API with real Fluent elements. All other test files use `package poly` (white-box).

### Testing with polytest

The `polytest` package provides a test harness for Handle functions without channels, transports, or goroutines:

```go
h := polytest.New(polytest.Config[State]{
    State:      State{Count: 0},
    Render:     render,
    Handle:     handle,
    Middleware: []polytest.Middleware[State]{withAuth},
    OnNavigate: onNavigate,
})

// Sending events
h.Send("increment")                              // click event
h.SendInput("search", "query")                   // input event with value
h.SendSubmit("save", map[string]string{"n": "v"}) // submit event with form data
h.SendEvent(poly.Event{...})                      // arbitrary event
h.Navigate("/users?id=42")                        // navigate event with URL params

// State and render
h.State()       // accumulated state
h.HTML()        // rendered HTML from last Send
h.Render()      // full GET render of current state
h.RenderNode()  // node tree for direct inspection

// Side-effect accessors
h.Toast()       // last toast message
h.URL()         // last navigated URL
h.Title()       // last title change
h.Announce()    // last accessibility announcement
h.Flash()       // last flash messages (map[string]string)
h.Signals()     // last signal values (map[string]any)

// Assertion helpers
h.HasToast("Saved")              // toast matches text
h.HasSignal("count", float64(1)) // signal matches key and value
h.HasAnnounce("Done")            // announcement matches text
h.HasFlash("#msg", "Saved")      // flash matches selector and text
h.URLWasReplaced()               // last URL used ReplaceURL (not Navigate)
```

`polytest.Middleware` wraps `PreSession`-based handlers (same signature as `PageConfig.Handle`):

```go
type HandleFunc[S any] func(poly.PreSession, S, poly.Event) S
type Middleware[S any] func(next HandleFunc[S]) HandleFunc[S]
```

## Client JS

`client/fluent-poly.js` is plain JS with no build step or bundler. It uses `Idiomorph.morph()` from the bundled `idiomorph.min.js` (0BSD licence). Both are embedded via `go:embed` in `embed.go` and served by `ServeClient()`.

The JS exposes one global: `window.Poly` with a `hooks` property for JS interop and an optional `onError` callback for error reporting.

Supported data attributes:

**Server events:** `data-poly-click`, `data-poly-input`, `data-poly-submit`, `data-poly-change`, `data-poly-keydown`, `data-poly-filter-key`, `data-poly-focus`, `data-poly-blur`, `data-poly-viewport`

**Navigation:** `data-poly-link`

**Client-side toggles:** `data-poly-toggle-class`, `data-poly-toggle-target`, `data-poly-toggle-attr`

**Signal bindings:** `data-poly-bind-text`, `data-poly-bind-show`, `data-poly-bind-hide`, `data-poly-bind-class`, `data-poly-bind-attr`, `data-poly-bind-value`

**Signal directives:** `data-poly-toggle-signal`, `data-poly-set-signal`, `data-poly-optimistic`, `data-poly-optimistic-toggle`

**Timing:** `data-poly-debounce`, `data-poly-throttle`

**UX:** `data-poly-disable`, `data-poly-confirm`, `data-poly-autofocus`, `data-poly-preserve`, `data-poly-indicator`, `data-poly-focus-trap`, `data-poly-cloak`, `data-poly-permanent`

**Uploads:** `data-poly-upload`

**Lifecycle:** `data-poly-hook`, `data-poly-transition`

**Configuration (set by server, read by JS):** `data-poly-retry-delay`, `data-poly-max-retry-delay`, `data-poly-debounce-default`, `data-poly-transition-timeout`, `data-poly-dev`

**Developer warnings:** The client reports an error (via `Poly.onError` if set, otherwise `console.warn`) if a patch or morph contains multiple root elements. Only the first element is used — wrap siblings in a container to avoid silent data loss.

**Service worker:** `data-poly-worker` (boolean — registers the service worker), `data-poly-push-key` (VAPID public key for push subscription)

**Dev mode:** `data-poly-dev` (boolean — disables service worker, reloads on disconnect)

**Internal (managed by JS):** `data-poly-client-classes`, `data-poly-client-attrs`

## Health check

`Handler.Health()` returns a `HealthStatus` struct with `Pending`, `Active`, and `Disconnected` counts. Reads three map lengths under a single lock acquisition. The struct has `json` tags for direct serialisation. Safe to call from any goroutine.

## Graceful drain

`Handler.Drain(ctx)` stops accepting new sessions while letting existing ones finish naturally. New page loads receive 503. Reconnecting clients can still reattach to their disconnected sessions. Per-session timers continue running so idle and lifetime limits are enforced during the drain period.

The method blocks until all pools (pending, active, disconnected) are empty or `ctx` is cancelled. Internally it polls `Health()` every 500ms. After `Drain` returns, call `Shutdown` to cancel remaining sessions and stop the pending cleanup goroutine.

The `draining` flag is an `atomic.Bool` on `Handler`. It is checked at the top of `serveInitialPage` and in the fresh-session fallback path of `serveSession`. The disconnected-session and pending-session paths are not gated — reconnects and pending claims still work during drain.

## Live region announcements

`Session.Announce(text)` sends text to a screen-reader-accessible `aria-live="polite"` region on the client. The JS runtime lazily creates a visually hidden `<div role="status" aria-live="polite" aria-atomic="true">` and sets its `textContent`. The `announce` field is part of the `Update`/`UpdateMessage` wire format (`"announce"` JSON key, `omitempty`).

To trigger re-announcement of identical text, the JS clears the region first then sets the text in the next animation frame.

## Dev mode

`Config.DevMode` (or `POLY_DEV` env var) optimises the development experience:

- **No service worker** — the client unregisters any existing worker and skips registration, ensuring fresh assets on every reload.
- **Page reload on disconnect** — instead of reconnecting with exponential backoff, the page does `location.reload()` after a brief delay. When the Go server restarts, the browser reloads with fresh state automatically.
- **Cache-Control: no-store** — prevents the browser from serving a cached initial page with a stale session ID.

The `DevMode` bool takes precedence over the environment variable. When `DevMode` is false (the default), `os.Getenv("POLY_DEV")` is checked as a fallback. The reconnect bar shows "Reloading…" instead of "Reconnecting…" in dev mode.

## Error reporting

`Poly.onError` is an optional callback on the global `window.Poly` object. When set, it receives `{type, message}` for every error or warning in the client JS runtime.

Error types:
- `"parse"` — JSON parse failure on WebSocket or SSE message
- `"fetch"` — Network failure for SSE POST events or event replay
- `"worker"` — Service worker registration failure
- `"push"` — Push subscription or subscription POST failure
- `"indexeddb"` — IndexedDB queue or replay failure
- `"render"` — Patch or morph contains multiple root elements

When `Poly.onError` is not set, non-silent errors fall through to `console.warn` and silent errors (parse, indexeddb, fetch) are swallowed. The `reportError(type, message, silent)` helper inside the IIFE handles the routing.

## Service worker

`client/poly-worker.js` is registered by the client JS when `data-poly-worker` is present on the root element. Set `Config.Worker = true` (or configure `Push`) to enable it.

The service worker provides:

- **Asset caching:** Cache-first for `/_poly/*` GET requests (JS runtime files). On install, precaches `fluent-poly.js` and `idiomorph.min.js`, plus any extra URLs passed to `ServeClient(precache ...string)`.
- **Page caching:** Network-first for navigation requests. Caches successful HTML responses; serves the cached version when offline.
- **Push event handling:** Receives push messages and shows notifications via `showNotification()`. Handles `notificationclick` for URL navigation.
- **Background sync:** Replays failed SSE POST events from IndexedDB when connectivity returns (Chromium only; other browsers replay on tab reconnect).

Cache is keyed by a content hash of the embedded files (injected at serve time). Old caches are deleted on activate.

### Reconnecting indicator

A fixed bar at the top of the viewport shows "Reconnecting…" when the transport connection drops. It applies to all transport modes regardless of the `Worker` setting. Styled with inline styles, overridable via the `.poly-reconnecting` class. Uses `role="status"` and `aria-live="polite"` for accessibility.

## Push notifications

`Config.Push` enables Web Push support. Setting it implicitly enables `Worker`.

**Go types:**

```go
type PushConfig[S any] struct {
    Sender      *push.Sender
    OnSubscribe func(session *Session[S], sub push.Subscription)
}
```

The `push` subpackage defines:

```go
type Subscription struct {
    Endpoint string
    Keys     SubscriptionKeys
}

type SubscriptionKeys struct {
    P256dh string
    Auth   string
}

type Notification struct {
    Title    string
    Body     string
    Icon     string
    Badge    string
    URL      string
    Tag      string
    Renotify bool
    Silent   bool
    Actions  []NotificationAction
}
```

**Setup:**

```go
sender := push.NewSender(push.Config{
    VAPIDPublicKey:  publicKey,
    VAPIDPrivateKey: privateKey,
    Subject:         "mailto:admin@example.com",
})

poly.New(poly.Config[State]{
    Push: &poly.PushConfig[State]{
        Sender: sender,
        OnSubscribe: func(sess *poly.Session[State], sub push.Subscription) {
            // Store subscription in your database
        },
    },
    // ...
})
```

**Subscription flow:**

1. Client JS reads `data-poly-push-key` from the root element
2. Calls `pushManager.subscribe()` with the VAPID key
3. POSTs subscription JSON to the poly endpoint with `X-Poly-Push-Subscribe: true` and `X-Poly-Session` headers
4. Server calls `OnSubscribe(session, sub)` in a goroutine

**Sending notifications:**

```go
sess.Push(push.Notification{
    Title: "New message",
    Body:  "You have a new message from Alice",
    URL:   "/messages",
})
```

The `push.Sender` handles ECDH key agreement, HKDF key derivation (`golang.org/x/crypto/hkdf`), AES-128-GCM payload encryption, and VAPID JWT signing. Returns `push.ErrSubscriptionExpired` for HTTP 410 responses.

## Event resilience (SSE)

In SSE mode, failed POST events are queued in IndexedDB (`poly-events` object store) and replayed on reconnect. The replay happens before the navigate event so the server processes queued events first.

When the service worker is active and Background Sync is available, a `poly-event-sync` sync tag is registered so queued events replay even if the tab was closed. Events older than 60 seconds are discarded as stale.

IndexedDB helpers (`openEventDB`, `queueFailedEvent`, `drainEvents`) are shared between the main thread and the service worker.

## Dependencies

- `github.com/coder/websocket` — WebSocket library, used only in `ws/` sub-package.
- `github.com/jpl-au/fluent` — HTML node tree library
- `github.com/jpl-au/fluent-jit` — diff engine for dynamic nodes
- `golang.org/x/crypto` — HKDF key derivation, used only in `push/` sub-package

## Security

- **Origin checking:** `Config.Security.AllowedOrigins` provides a single configuration point for origin enforcement across all transport types (WebSocket upgrades, SSE streams, and POST events). When set, the `Origin` header must match one of the listed values exactly. When empty, the handler falls back to same-host checking (the Origin host must match the request Host header) as basic CSRF protection. `ws.Upgrade()` skips the websocket library's own origin check because the poly handler enforces it first.
- Event data comes from the client — always validate in the `Handle` function.
- **Session IDs** are cryptographically random strings (`crypto/rand.Text`). They appear in query parameters for WebSocket upgrades and SSE streams, and in the `X-Poly-Session` header for POST events. Treat them as bearer tokens.
- **Referrer-Policy:** The initial page response sets `Referrer-Policy: same-origin` to prevent session ID leakage via the Referer header on external links.
- **Session ID in logs:** Session IDs appear in server access logs as query parameters. If log exposure is a concern, consider stripping query strings from access logs or using a reverse proxy that redacts them.

## Known limitations

- **MaxSessions counts pending + active only.** Disconnected sessions are excluded from the limit, so a network blip does not block new connections while clients reconnect.
- **Keep `Render` functions fast.** A slow `Render` will block concurrent `Session.Update` calls on the same session.
- **Loading state restoration:** All disabled elements are re-enabled when any server update arrives. If two events are in flight simultaneously, the response for one will re-enable the element disabled by the other. In practice this is rare because events are debounced/throttled.
- **Client state vs server state:** Client-side toggles (`ToggleClass`, `ToggleAttr`) modify the DOM without the server knowing. Client-managed state survives server morphs, but if the server explicitly sets the same class or attribute, the client value takes precedence.
