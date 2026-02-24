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
poly/           Root — Transport, Session, Config, Event, Group, protocol, bind helpers
poly/ws/        WebSocket transport (only package importing coder/websocket)
poly/sse/       SSE+POST transport (no external dependencies)
poly/client/    Embedded JS files (fluent-poly.js, idiomorph.min.js)
```

Source files in the root package, split by concern:

```
handler.go      Public API — Config, New(), ServeHTTP, ServeClient
pool.go         Internal session lifecycle — pools, reap, reattach
page.go         Initial page rendering — polyBody, newID
session.go      Single session — event loop, Update, Navigate, SetTitle
group.go        Broadcasting — Group type for multi-session updates
protocol.go     Wire format types and encoding
transport.go    Transport and EventPusher interfaces
event.go        Event and Params types
bind.go         Generic event binding helpers
embed.go        Client JS embedding
```

Transport implementations live in sub-packages. The `Config.Upgrade` field accepts any function that returns a `Transport`, keeping the root package transport-agnostic. `Config.Fallback` provides a secondary transport (typically SSE) used when the primary is unavailable.

## Event flow

1. Client JS sends a DOM event as JSON: `{"type":"click","action":"increment","data":{}}`
2. `Transport.ReceiveEvent()` deserialises it to an `Event`
3. `Session.safeHandleEvent()` calls the user's `Handle` function with the current state (wrapped in panic recovery)
4. The returned state is rendered to a new node tree and diffed against the previous render
5. `Transport.SendUpdate()` sends a unified update message containing either:
   - **Patches** — targeted content updates for keyed elements that changed
   - **Morphs** — structural DOM changes (e.g. root morph when keys are added/removed/reordered)
6. Client JS applies patches first (targeted `Idiomorph.morph` on keyed elements), then morphs (root or scoped `Idiomorph.morph`). All DOM writes are batched inside a single `requestAnimationFrame` callback so the browser coalesces reflows into one paint pass

### Panic recovery

If `Handle` or `Render` panics during event processing, `safeHandleEvent` recovers the panic, logs it with the session ID and action, and drops the event. The session continues processing subsequent events. `Session.Update` has the same recovery — a panicking callback does not crash the caller's goroutine.

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

The `change` field comes from `jit.StructuralChange.String()`, which reports exactly which keys were added, removed, or if they were reordered. The `tip` guides the developer toward wrapping conditional elements in a stable keyed container to avoid full-page morphs.

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

The callback runs under the session lock, so keep it fast — offload expensive work to a goroutine. The `StructuralChange` struct has `Added`, `Removed` (key slices), `Reordered` (bool), and `Bytes` (re-rendered HTML size).

## Event binding

There are two equivalent ways to bind events to elements. Both produce the same `data-poly-*` attribute:

```go
// Helper (convenience — wraps SetData with the correct convention string)
poly.Click(button.Text("+"), "increment")

// Direct (explicit — useful when you need full control)
button.Text("+").SetData("poly-click", "increment")
```

The generic helpers (`Click`, `Submit`, `Input`, `Change`, `KeyDown`, `Focus`, `Blur`) are defined in `bind.go`. They use a structural type constraint so they work with any Fluent element without coupling the two packages.

### Keydown modifiers

Keydown events include modifier key state in `Event.Data`: `ctrl`, `shift`, `alt`, `meta` are set to `"true"` when held. This enables keyboard shortcut handling:

```go
func handle(s state, ev poly.Event) state {
    if ev.Action == "shortcut" && ev.Data["ctrl"] == "true" && ev.Data["key"] == "s" {
        // Ctrl+S pressed
    }
    return s
}
```

### Timing control

Input events are debounced at `DefaultDebounce` (default 300ms). Override per element:

```go
poly.Debounce(poly.Input(input.Text("q", ""), "search"), 150) // 150ms
```

Throttle any event type:

```go
poly.Throttle(poly.Click(button.Text("Fire"), "fire"), 1000) // max once per second
```

### Loading states

Elements with `data-poly-disable` are disabled while an event is in flight and restored when the next server update arrives:

```go
poly.Disable(poly.Click(button.Text("Save"), "save"), "Saving...")
```

If the text argument is non-empty, the element's text content is temporarily replaced. The JS runtime tracks pending elements in an array and restores them at the top of `applyMessage`, before patches/morphs are applied.

### Confirmation dialogs

Elements with `data-poly-confirm` show `window.confirm` before the event is sent:

```go
poly.Confirm(poly.Click(button.Text("Delete"), "delete"), "Are you sure?")
```

If the user cancels, the event is dropped entirely.

### Focus management

Elements with `data-poly-focus` receive focus after patches and morphs are applied:

```go
poly.AutoFocus(input.Text("name", ""))
```

The JS runtime calls `focus()` on the first `[data-poly-focus]` element at the end of `applyMessage`. Only one element should have this attribute at a time.

### URL routing

Bidirectional sync between Go state and the browser URL. Anchors with `data-poly-link` are intercepted by the JS runtime — instead of a full page load, the URL is pushed via `history.pushState` and a navigate event is sent to the server.

```go
// Config — HandleParams processes URL changes on initial load and navigation
HandleParams: func(state State, params poly.Params) State {
    state.Page = params.Path
    return state
},

// Mark an anchor for client-side navigation
poly.Link(a.Link("/profile", "Profile"))

// Equivalent using SetData directly
a.Link("/profile", "Profile").SetData("poly-link", "")
```

`HandleParams` is called:
1. On initial page load (after `InitialState`), with the request URL
2. On link clicks within `[data-poly-link]` anchors
3. On browser back/forward (popstate)

If `HandleParams` is nil, navigation events fall through to `Handle` as `Event{Type: "navigate", Data: {"path": "...", "search": "..."}}`.

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
poly.ToggleClass(button.Text("Menu"), "is-open")

// Toggle a CSS class on a different element
poly.ToggleClass(poly.ToggleTarget(button.Text("Menu"), "#nav"), "is-open")

// Toggle visibility via the hidden attribute
poly.ToggleAttr(poly.ToggleTarget(button.Text("Show Help"), "#help"), "hidden")
```

Helpers: `ToggleClass`, `ToggleTarget`, `ToggleAttr`. Data attributes: `data-poly-toggle-class`, `data-poly-toggle-target`, `data-poly-toggle-attr`.

Client-managed state survives server morphs via an Idiomorph `beforeNodeMorphed` hook. The JS runtime tracks which classes and attributes are client-managed using `data-poly-client-classes` and `data-poly-client-attrs` on the target element. If the element is removed entirely (not morphed), the client state is lost — this is by design.

**Performance:** The generic helpers are ~47% slower than raw `SetData` and add 2 extra allocations per element. Rendered output is identical once built — the overhead is purely in element creation, caused by Go's shape-based generic dispatch preventing full inlining. For performance-sensitive code, prefer `SetData` directly. Run `go test -bench=BenchmarkBind -benchmem` to compare.

**PGO:** [Profile-Guided Optimization](https://go.dev/doc/pgo) improves speed ~5-10% for both generic and direct paths but cannot eliminate the allocation gap — that's structural to Go's shape-based generics. Applications consuming fluent-poly should collect a CPU profile from production and place it as `default.pgo` in their main package. Do not commit a `default.pgo` into this library — PGO profiles are application-specific.

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
func handle(s state, ev poly.Event) state {
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
      poly.Preserve(form.New(/*...*/)),
      span.Text(s.TodoError).Style("color: #c33"),
  ).Dynamic("todo-form")
  ```

- **`poly.Preserve`** prevents the JS runtime from resetting form fields after submit. Without it, `target.reset()` clears the user's input before the server morph arrives — losing the text on validation failure.

- **Live validation via `poly.Input`** with a dedicated action (e.g. `"validate-todo"`) gives the user feedback as they type, debounced at 300ms.

- **Keep error spans always in the tree** (empty text when no error) to avoid structural changes that trigger root morphs. Toggling between "error present" and "no error" should be a content patch, not a structural one.

## JS hooks

Elements with `data-poly-hook` receive JavaScript lifecycle callbacks, enabling integration with third-party libraries (charts, maps, rich text editors):

```go
poly.Hook(div.New(), "chart")
```

```js
Poly.hooks.chart = {
    mounted: function(el) { /* initialise — el is in the DOM */ },
    updated: function(el) { /* re-render — el was morphed */ },
    destroyed: function(el) { /* teardown — el is about to be removed */ }
};
```

The global `Poly.hooks` object is defined before the IIFE in `fluent-poly.js`. The `callHook` function checks for the `data-poly-hook` attribute and calls the appropriate lifecycle method. All three callbacks are optional.

**Lifecycle timing:**
- `mounted` fires in `afterNodeAdded` — the element is in the DOM
- `updated` fires in `afterNodeMorphed` — the element was morphed in place
- `destroyed` fires in `beforeNodeRemoved` — the element is still in the DOM but about to be removed. For elements with transitions, destroyed fires before the leave transition starts.

## Transitions

Elements can opt into CSS transitions during morph-driven add/remove by setting `data-poly-transition` (or using the `poly.Transition` helper):

```go
poly.Transition(div.New(children...), "fade")
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

group.Broadcast(func(s State) State {
    s.Notification = "System update"
    return s
})
```

`Broadcast` snapshots the session pointers before calling `Update` on each, so the group lock is not held while acquiring session locks. `Add`, `Remove`, `Broadcast`, and `Len` are all safe to call from any goroutine.

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

// SSE with larger event buffer for high-frequency streams
poly.New(poly.Config[State]{
    Mode:     poly.SSEOnly,
    Fallback: sse.Upgrade(64),
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

When SSE is active, events are sent via `fetch(url, {method: "POST"})` to the same endpoint with `?session=ID`. The `EventPusher` interface (in `transport.go`) is implemented by transports that receive events externally rather than through the transport connection itself. The SSE transport implements it; the WebSocket transport does not.

**SSE buffer size:** `sse.Upgrade()` accepts an optional buffer size parameter (default 16). When the internal event channel is full, `PushEvent` returns `ErrEventBufferFull` and the HTTP handler responds with 429. Increase the buffer for high-frequency event streams: `sse.Upgrade(64)`.

**SSE reconnection:** `EventSource` has built-in reconnection. When the SSE connection drops, the session moves to the disconnected pool. On reconnect, the existing `reattach` flow sends a full re-render morph — no `Last-Event-ID` replay is needed.

**Wire format:** Identical JSON regardless of transport. Server updates are WebSocket messages or SSE `data:` lines. Client events are WebSocket frames or POST bodies. The same `Update` and `Event` types are used.

## Complete helper reference

| Helper | Data attribute | Purpose |
|--------|---------------|---------|
| `Click` | `poly-click` | Server event on click |
| `Submit` | `poly-submit` | Server event on form submit |
| `Input` | `poly-input` | Server event on input (debounced) |
| `Change` | `poly-change` | Server event on value commit |
| `KeyDown` | `poly-keydown` | Server event on keydown (with modifiers) |
| `Focus` | `poly-focus` | Server event on focus |
| `Blur` | `poly-blur` | Server event on blur |
| `Link` | `poly-link` | Client-side navigation |
| `ToggleClass` | `poly-toggle-class` | Client-side CSS class toggle |
| `ToggleTarget` | `poly-toggle-target` | Direct toggle at different element |
| `ToggleAttr` | `poly-toggle-attr` | Client-side boolean attribute toggle |
| `Debounce` | `poly-debounce` | Override input debounce delay (ms) |
| `Throttle` | `poly-throttle` | Minimum interval between events (ms) |
| `Disable` | `poly-disable` | Disable element while event in flight |
| `Confirm` | `poly-confirm` | Show confirmation before sending event |
| `Preserve` | `poly-preserve` | Prevent form reset after submit |
| `AutoFocus` | `poly-focus` | Focus element after server update |
| `Hook` | `poly-hook` | JS lifecycle callbacks |
| `Transition` | `poly-transition` | CSS enter/leave transitions |

## Code style

- Comments explain why, not what
- Names must not repeat their package context (`poly.Click` not `poly.PolyClick`)
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
group_test.go       Broadcasting
bind_test.go        Event binding helpers (package poly_test, black-box)
protocol_test.go    Wire format encoding
bench_test.go       Performance benchmarks
sse/sse_test.go     SSE transport
```

`mock_test.go` defines `mockTransport` (replays queued events, records sent `Update` values) and `newTestSession` (creates a session with a seeded differ). Helper functions `patchUpdates()` and `morphUpdates()` filter recorded updates by type. Use these for any new session behaviour tests.

`bind_test.go` uses `package poly_test` (black-box) because it verifies the public API with real Fluent elements. All other test files use `package poly` (white-box).

## Client JS

`client/fluent-poly.js` is plain JS with no build step or bundler. It uses `Idiomorph.morph()` from the bundled `idiomorph.min.js` (0BSD licence). Both are embedded via `go:embed` in `embed.go` and served by `ServeClient()`.

The JS exposes one global: `window.Poly` with a `hooks` property for JS interop.

Supported data attributes:

**Server events:** `data-poly-click`, `data-poly-input`, `data-poly-submit`, `data-poly-change`, `data-poly-keydown`, `data-poly-focus`, `data-poly-blur`

**Navigation:** `data-poly-link`

**Client-side toggles:** `data-poly-toggle-class`, `data-poly-toggle-target`, `data-poly-toggle-attr`

**Timing:** `data-poly-debounce`, `data-poly-throttle`

**UX:** `data-poly-disable`, `data-poly-confirm`, `data-poly-focus` (auto-focus), `data-poly-preserve`

**Lifecycle:** `data-poly-hook`, `data-poly-transition`

**Configuration (set by server, read by JS):** `data-poly-retry-delay`, `data-poly-max-retry-delay`, `data-poly-debounce-default`, `data-poly-transition-timeout`

**Internal (managed by JS):** `data-poly-client-classes`, `data-poly-client-attrs`

## Dependencies

- `github.com/coder/websocket` — WebSocket library, used only in `ws/` sub-package. Chosen for: idiomatic Go API, context-aware, zero transitive dependencies.
- `github.com/jpl-au/fluent` — HTML node tree library
- `github.com/jpl-au/fluent-jit` — diff engine for dynamic nodes

## Security

- **Origin checking:** `Config.AllowedOrigins` provides a single configuration point for origin enforcement across all transport types (WebSocket upgrades, SSE streams, and POST events). When set, the `Origin` header must match one of the listed values exactly. When empty, the handler falls back to same-host checking (the Origin host must match the request Host header) as basic CSRF protection. `ws.Upgrade()` skips the websocket library's own origin check because the poly handler enforces it first.
- Event data comes from the client — always validate in the `Handle` function.
- **Session IDs** are cryptographically random strings (`crypto/rand.Text`). They appear in query parameters for WebSocket upgrades and SSE streams, and in the `X-Poly-Session` header for POST events. Treat them as bearer tokens.
- **Referrer-Policy:** The initial page response sets `Referrer-Policy: same-origin` to prevent session ID leakage via the Referer header on external links.
- **Session ID in logs:** Session IDs appear in server access logs as query parameters. If log exposure is a concern, consider stripping query strings from access logs or using a reverse proxy that redacts them.

## Known limitations

These are architectural trade-offs, not bugs. They are documented here so future contributors understand the decisions.

- **MaxSessions counts pending + active only.** Disconnected sessions (waiting for client reconnect) are excluded from the limit because they hold no transport resources. This prevents a network blip from blocking new connections while clients wait to reconnect.
- **Session lock granularity:** The session mutex is held during the entire render/diff/send cycle. This is necessary for correctness (state, differ, and transport are coupled), but it means `Render` functions should be computationally cheap. A slow `Render` will block concurrent `Session.Update` calls.
- **WebSocket backpressure:** `SendUpdate` writes synchronously. If the client is slow to read, the write will block, stalling the session event loop. This is inherent to the synchronous transport interface. For most applications the browser reads faster than the server writes, so this is not a practical concern.
- **Loading state restoration:** `restorePending()` restores all disabled elements when any server update arrives. If two events are in flight simultaneously (A and B), the response for A will re-enable the element disabled by B. A full fix requires event correlation IDs (protocol change). In practice this is rare because events are debounced/throttled and server responses are fast.
- **Client state vs server state:** Client-side toggles (`ToggleClass`, `ToggleAttr`) modify the DOM without the server knowing. When a server morph arrives, the `beforeNodeMorphed` hook preserves client state. However, if the server explicitly sets the same class or attribute, the client value takes precedence. This is intentional for ephemeral UI state (menus, modals) but can be surprising if misused.
