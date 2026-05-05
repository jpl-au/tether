# WASM client runtime

> **Experimental.** This is an early proof of concept. Expect breaking
> changes, missing features, and rough edges. The default JS runtime
> (`tether.js`) remains the supported path for production apps.

Tether ships a Go WASM client runtime as an opt-in alternative to
`tether.js`. It runs a Go program compiled to WebAssembly in the
browser: decoding server updates, applying DOM patches, and posting
events back to the server. It drops in against an unmodified tether
server.

Today it is deliberately narrow. SSE only, click and submit events
only, no service worker, no signals, no IndexedDB queue, no file
uploads. For anything beyond the simplest demo, use the default JS
runtime.

## Selecting the runtime

Set `Client.Runtime` on your app:

```go
app := tether.App{
    Client: tether.Client{
        Runtime: tether.Runtime.WASM("/static/client.wasm"),
    },
}
```

The framework:

- Writes `data-tether-wasm-src` on the tether root element with the
  path you pass.
- Injects `tether-wasm.js` (the bootstrap loader) instead of
  `tether.js`.
- Skips the default idiomorph script.

The bootstrap derives the location of `wasm_exec.js` from the `.wasm`
blob's directory. For example, `Runtime.WASM("/static/client.wasm")`
loads `/static/wasm_exec.js`.

Leave `Client.Runtime` unset (or use `tether.Runtime.Default()`) for
the JS runtime.

## Wire format

The default wire format is JSON. The WASM client also accepts CBOR,
selected per handler:

```go
import "github.com/jpl-au/tether/wire"

handler := tether.Stateful(app, tether.StatefulConfig[State]{
    WireFormat: wire.CBOR,
    // ...
})
```

CBOR gives smaller payloads and faster decode. The framework writes
the chosen format to `data-tether-wire-format` on the root element;
the WASM client reads that attribute to pick the matching decoder.

The JS runtime only supports JSON. Do not set `wire.CBOR` on a handler
that uses the default runtime.

## Building the WASM blob

The runtime source lives alongside the worked example in
`fluent-examples` and ships build scripts for both stdlib Go and
TinyGo:

```bash
# Stdlib Go (easier, larger binary).
bash build-go.sh

# TinyGo (smaller binary, requires Go 1.25.x alongside).
bash build-tinygo.sh

# Both, with a size comparison.
bash build.sh
```

Output is `client.go.wasm` or `client.tinygo.wasm`. Copy it, along
with the matching `wasm_exec.js`, into your app's static directory.

TinyGo 0.40.1 requires Go 1.25.x. If your system Go is newer, install
a compatible toolchain:

```bash
go install golang.org/dl/go1.25.9@latest && go1.25.9 download
```

The TinyGo script picks this up automatically when it is on `PATH`.

### Size reference

PoC measurements against a JSON-only client:

| Build       | Raw    | Gzip   |
|-------------|--------|--------|
| stdlib Go   | 3.3 MB | 930 KB |
| TinyGo      | 985 KB | 352 KB |
| `tether.js` | 74 KB  | 20 KB  |

These are a baseline, not a commitment. The runtime is small today
because it does very little; numbers will move as features are added.
CBOR support currently costs roughly 220 KB gzipped on top of the
figures above due to reflection-based encoding.

## Working example

`fluent-examples/tether-wasm/` is a minimal end-to-end example:
a tether handler wired with `Runtime.WASM(...)`, a `build.sh` that
compiles the WASM and drops the artefacts into `static/`, and a page
that exercises click and submit bindings. It is the easiest way to
see the runtime in context and the recommended starting point.

## What works, what does not

**Works:**

- SSE transport
- Click and submit event bindings
- JSON and CBOR wire formats
- DOM patches and morphs applied via `syscall/js`
- Drops in against an unmodified tether server

**Does not work yet:**

- WebSocket transport
- Client-side directives (toggle, scroll, transitions)
- Signals and client-computed state
- Service worker, push notifications
- IndexedDB event queue, background sync
- File uploads, drag-and-drop
- Client-side navigation, flash, toasts, aria-live, hotkeys

## Direction

Rewriting all of `tether.js` in Go is likely not the right path. JS is
the natural language for DOM manipulation; `syscall/js` adds friction
without adding value for that workload.

The more plausible path forward is a hybrid runtime where JS handles
the DOM and WASM handles the data layer - wire decoding, shared
types, and heavier client-side computation. The `ClientRuntime`
interface is designed to accommodate that.

This is active R&D. The API shape, the scope of WASM's role, and
whether the WASM runtime ultimately becomes a first-class framework
offering are all open questions.
