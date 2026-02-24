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
poly/           Root — Transport, Session, Config, Event, protocol, bind helpers
poly/ws/        WebSocket transport (only package importing coder/websocket)
poly/sse/       SSE+POST transport (no external dependencies)
poly/client/    Embedded JS files (fluent-poly.js, idiomorph.min.js)
```

Transport implementations live in sub-packages. The `Config.Upgrade` field accepts any function that returns a `Transport`, keeping the root package transport-agnostic. `Config.Fallback` provides a secondary transport (typically SSE) used when the primary is unavailable.

## Event flow

1. Client JS sends a DOM event as JSON: `{"type":"click","action":"increment","data":{}}`
2. `Transport.ReceiveEvent()` deserialises it to an `Event`
3. `Session.handleEvent()` calls the user's `Handle` function with the current state
4. The returned state is rendered to a new node tree and diffed against the previous render
5. `Transport.SendUpdate()` sends a unified update message containing either:
   - **Patches** — targeted content updates for keyed elements that changed
   - **Morphs** — structural DOM changes (e.g. root morph when keys are added/removed/reordered)
6. Client JS applies patches first (targeted `Idiomorph.morph` on keyed elements), then morphs (root or scoped `Idiomorph.morph`)

### Wire format

All updates use a single `"update"` message type:

```json
{"type":"update","patches":[{"key":"count","html":"<span>43</span>"}]}
{"type":"update","morphs":[{"key":"","html":"<div>...</div>"}]}
```

An empty `key` in morphs targets the root element. A non-empty key targets a specific keyed container (for scoped morphs).

### Structural change diagnostics

When a structural change is detected (keys added, removed, or reordered), the session logs a warning with actionable details:

```
WARN structural change, sending root morph session=abc change="key 'help' added" bytes=15234 tip="wrap conditional elements in a keyed container to scope this morph"
```

The `change` field comes from `jit.StructuralChange.String()`, which reports exactly which keys were added, removed, or if they were reordered. The `tip` guides the developer toward wrapping conditional elements in a stable keyed container to avoid full-page morphs.

## Event binding

There are two equivalent ways to bind events to elements. Both produce the same `data-poly-*` attribute:

```go
// Helper (convenience — wraps SetData with the correct convention string)
poly.Click(button.Text("+"), "increment")

// Direct (explicit — useful when you need full control)
button.Text("+").SetData("poly-click", "increment")
```

The generic helpers (`Click`, `Submit`, `Input`, `Change`, `KeyDown`, `Focus`, `Blur`) are defined in `bind.go`. They use a structural type constraint so they work with any Fluent element without coupling the two packages.

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

**Server-initiated URL updates:**

```go
// Push a new URL (adds history entry)
session.Navigate("/success")

// Replace current URL (no history entry)
session.ReplaceURL("/current?saved=true")
```

URL updates can accompany DOM patches/morphs in the same message, or be sent standalone.

**Wire format — navigate event from client:**

```json
{"type":"navigate","action":"","data":{"path":"/profile","search":"tab=settings"}}
```

**Wire format — URL command from server:**

```json
{"type":"update","url":"/profile","replace":false}
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

**Fallback timeout:** If `transitionend` never fires (no CSS transition defined, `display: none`, etc.), the node is removed after 5 seconds rather than leaking.

**Leave cancellation:** If a morph arrives that includes an element currently playing a leave transition, the leave is cancelled and the element is morphed normally. This handles rapid state changes (e.g. user toggles something twice quickly).

## SSE transport

An alternative to WebSocket for environments where proxies strip upgrade headers or WebSocket connections are unreliable. Server→client updates are sent as Server-Sent Events; client→server events are sent as HTTP POST.

```go
// WebSocket primary, SSE fallback
poly.New(poly.Config[State]{
    Upgrade:  ws.Upgrade(),
    Fallback: sse.Upgrade(),
    // ...
})
```

When `Fallback` is set, the handler accepts three additional request types:
- **GET + `Accept: text/event-stream`** — opens an SSE stream via the fallback upgrade function
- **POST + `?session=ID`** — routes the JSON event body to the active session's transport via the `EventPusher` interface
- **Initial HTML** — includes `data-poly-sse` on the root element so the client JS knows SSE is available

The `EventPusher` interface (in `transport.go`) is implemented by transports that receive events externally rather than through the transport connection itself. The SSE transport implements it; the WebSocket transport does not.

**Client fallback:** The JS runtime tries WebSocket first. If the first connection attempt fails and `data-poly-sse` is present on the root element, it switches to SSE+POST permanently for that page load. In SSE mode, events are sent via `fetch(url, {method: "POST"})` to the same endpoint with `?session=ID`.

**SSE reconnection:** `EventSource` has built-in reconnection. When the SSE connection drops, the session moves to the disconnected pool. On reconnect, the existing `reattach` flow sends a full re-render morph — no `Last-Event-ID` replay is needed.

**Wire format:** Identical JSON to WebSocket. Server updates are SSE `data:` lines. Client events are POST bodies. The same `Update` and `Event` types are used regardless of transport.

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

Tests live alongside the code they test (`session_test.go`, `protocol_test.go`, `bind_test.go`). The `bind_test.go` tests use `package poly_test` (black-box) because they verify the public API with real Fluent elements.

`session_test.go` uses a `mockTransport` that replays queued events and records sent `Update` values. Helper functions `patchUpdates()` and `morphUpdates()` filter recorded updates by type for assertions. Use this pattern for any new session behaviour tests.

## Client JS

`client/fluent-poly.js` is plain JS with no build step or bundler. It uses `Idiomorph.morph()` from the bundled `idiomorph.min.js` (0BSD licence). Both are embedded via `go:embed` in `embed.go` and served by `ServeClient()`.

Supported event bindings (data attributes): `data-poly-click`, `data-poly-input`, `data-poly-submit`, `data-poly-change`, `data-poly-keydown`, `data-poly-focus`, `data-poly-blur`, `data-poly-link`.

Input events are debounced at 300ms by default, configurable per element via `data-poly-debounce="500"`. Throttling is available via `data-poly-throttle="1000"`.

## Dependencies

- `github.com/coder/websocket` — WebSocket library, used only in `ws/` sub-package. Chosen for: idiomatic Go API, context-aware, zero transitive dependencies.
- `github.com/jpl-au/fluent` — HTML node tree library
- `github.com/jpl-au/fluent-jit` — diff engine for dynamic nodes

## Security

- `ws.Upgrade()` sets `InsecureSkipVerify: true` for development. Pass origin patterns for production: `ws.Upgrade("https://example.com")`.
- Event data comes from the client — always validate in the `Handle` function.
