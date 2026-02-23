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
poly/client/    Embedded JS files (fluent-poly.js, idiomorph.min.js)
```

Transport implementations live in sub-packages. The `Config.Upgrade` field accepts any function that returns a `Transport`, keeping the root package transport-agnostic. New transports (e.g. SSE) should follow the `ws/` pattern: a sub-package exporting a single `Upgrade` function.

## Event flow

1. Client JS sends a DOM event as JSON: `{"type":"click","action":"increment","data":{}}`
2. `Transport.ReceiveEvent()` deserialises it to an `Event`
3. `Session.handleEvent()` calls the user's `Handle` function with the current state
4. The returned state is rendered to a new node tree and diffed against the previous render
5. If keys changed (structural): `Transport.SendFull()` sends the full HTML
6. If only values changed: `Transport.SendPatches()` sends targeted patches
7. Client JS morphs the DOM in place via idiomorph

## Event binding

There are two equivalent ways to bind events to elements. Both produce the same `data-poly-*` attribute:

```go
// Helper (convenience — wraps SetData with the correct convention string)
poly.Click(button.Text("+"), "increment")

// Direct (explicit — useful when you need full control)
button.Text("+").SetData("poly-click", "increment")
```

The generic helpers (`Click`, `Submit`, `Input`, `Change`, `KeyDown`, `Focus`, `Blur`) are defined in `bind.go`. They use a structural type constraint so they work with any Fluent element without coupling the two packages.

**Performance:** The generic helpers are ~47% slower than raw `SetData` and add 2 extra allocations per element. Rendered output is identical once built — the overhead is purely in element creation, caused by Go's shape-based generic dispatch preventing full inlining. For performance-sensitive code, prefer `SetData` directly. Run `go test -bench=BenchmarkBind -benchmem` to compare.

**PGO:** [Profile-Guided Optimization](https://go.dev/doc/pgo) improves speed ~5-10% for both generic and direct paths but cannot eliminate the allocation gap — that's structural to Go's shape-based generics. Applications consuming fluent-poly should collect a CPU profile from production and place it as `default.pgo` in their main package. Do not commit a `default.pgo` into this library — PGO profiles are application-specific.

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

`session_test.go` uses a `mockTransport` that replays queued events and records sent messages. Use this pattern for any new session behaviour tests.

## Client JS

`client/fluent-poly.js` is plain JS with no build step or bundler. It uses `Idiomorph.morph()` from the bundled `idiomorph.min.js` (0BSD licence). Both are embedded via `go:embed` in `embed.go` and served by `ServeClient()`.

Supported event bindings (data attributes): `data-poly-click`, `data-poly-input`, `data-poly-submit`, `data-poly-change`, `data-poly-keydown`, `data-poly-focus`, `data-poly-blur`.

Input events are debounced at 300ms by default, configurable per element via `data-poly-debounce="500"`. Throttling is available via `data-poly-throttle="1000"`.

## Dependencies

- `github.com/coder/websocket` — WebSocket library, used only in `ws/` sub-package. Chosen for: idiomatic Go API, context-aware, zero transitive dependencies.
- `github.com/jpl-au/fluent` — HTML node tree library
- `github.com/jpl-au/fluent-jit` — diff engine for dynamic nodes

## Security

- `ws.Upgrade` currently sets `InsecureSkipVerify: true` for development. Production deployments must configure allowed origins.
- Event data comes from the client — always validate in the `Handle` function.
