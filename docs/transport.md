# Transport

## Transport mode

Control which transports the handler accepts with the `Mode` field.
When `Mode` is not set, it defaults to `mode.Both`.

```go
// Default (mode.Both) — WebSocket with SSE fallback
tether.Live(tether.LiveConfig[State]{
    Upgrade:  ws.Upgrade(),
    Fallback: sse.Upgrade(),
    // ...
})

// WebSocket only
tether.Live(tether.LiveConfig[State]{
    Mode:    mode.WebSocket,
    Upgrade: ws.Upgrade(),
    // ...
})

// SSE only
tether.Live(tether.LiveConfig[State]{
    Mode:     mode.ServerSentEvents,
    Fallback: sse.Upgrade(),
    // ...
})
```

Same wire format, same API regardless of transport. The encoding is
selected via `LiveConfig.WireFormat` (default `wire.JSON`).

## Protocol awareness

The framework detects whether each request arrives over HTTP/1.1 or
HTTP/2 and adapts accordingly. By default (`protocol.Auto`), detection
is automatic — the developer does nothing and it works correctly.

```go
import "github.com/jpl-au/tether/protocol"

// Explicit: tell the framework the environment is HTTP/2.
tether.Live(tether.LiveConfig[State]{
    Protocol: protocol.HTTP2,
    // ...
})
```

When set explicitly, the framework trusts the configuration and emits
a warning on every request where the wire protocol doesn't match:

```
WARN tether: protocol mismatch  configured=HTTP/2 detected=HTTP/1.1
```

This catches misconfigurations — e.g. setting `protocol.HTTP2` when a
reverse proxy downgrades to HTTP/1.1 — without rejecting the request.

The protocol can also be set via the `TETHER_PROTO` environment
variable (`HTTP1`, `HTTP2`, `HTTP3`, `AUTO`). Explicit
`LiveConfig.Protocol` takes precedence over the env var.

Protocol awareness applies to live sessions only — `tether.Page` is
stateless request/response and does not benefit from protocol-specific
behaviour.

### WebSocket options

Pass `ws.Options` to configure the WebSocket transport:

```go
ws.Upgrade(ws.Options{
    ReadLimit: 128 << 10,  // max message size (default 32 KB)
})
```

Set `ReadLimit` to match `LiveConfig.Limits.MaxEventBytes` for consistent limits across transport modes. Messages exceeding the limit cause the connection to be closed with a protocol error.

### Compression

WebSocket per-message deflate (RFC 7692) is **enabled by default**. The
browser negotiates the extension during the handshake and handles
decompression transparently — no client-side code is needed.

Default settings (zero value of `ws.Compression`):

| Setting | Default | Effect |
|---------|---------|--------|
| Level | `CompressionFastest` (1) | Lowest CPU, good ratios for HTML |
| Threshold | 512 bytes | Tiny messages skip compression |
| ContextTakeover | false | Shared compressor pool, fixed memory |

To disable compression (e.g. when a reverse proxy already compresses):

```go
ws.Upgrade(ws.Options{
    Compression: ws.Compression{Disabled: true},
})
```

To enable context takeover for better ratios on repetitive HTML
fragments (costs ~4 KB per connection instead of a fixed pool):

```go
ws.Upgrade(ws.Options{
    Compression: ws.Compression{ContextTakeover: true},
})
```

Compression levels:

- `ws.CompressionFastest` — least CPU, best for real-time (default)
- `ws.CompressionBalanced` — middle ground
- `ws.CompressionSmallest` — smallest payloads, highest CPU

SSE compression is handled by the reverse proxy (nginx, Caddy,
Cloudflare) via standard `Content-Encoding` negotiation — tether does
not need any configuration for it.

### Connection state

The tether root element exposes transport lifecycle via
`data-tether-state`. The attribute is set automatically by the client
JS — no configuration needed.

| Value | Meaning |
|-------|---------|
| `connecting` | Transport is attempting to connect (initial or reconnect) |
| `connected` | WebSocket or SSE stream is open and ready |
| `disconnected` | Connection lost, will retry |

Stateless pages (`tether.Page` / `mode.HTTP`) are immediately
`connected` since there is no persistent transport.

Use it in CSS to style elements based on connection state:

```css
[data-tether-state="connecting"] .submit-btn { opacity: 0.5; }
[data-tether-state="disconnected"] .status { color: red; }
```

### SSE keep-alive

SSE connections send keep-alive comments at `Timeouts.Heartbeat` (default 20s) to prevent proxies from closing idle connections.

## Service worker

Enable the service worker for asset caching and offline page shells:

```go
tether.Live(tether.LiveConfig[State]{
    Worker: true,
    // ...
})
```

The service worker caches the JS runtime (`tether.js`, `idiomorph.min.js`) using a cache-first strategy. Navigation responses are only cached when the server sends the `X-Tether-Cache: true` header — this prevents caching sensitive or session-specific pages without explicit intent. Cached pages are served as a fallback when offline.

To precache application assets (CSS, icons, fonts), use the `Precache` field on `Asset`:

```go
var assets = &tether.Asset{
    FS:       staticFS,
    Prefix:   "/static/",
    Precache: []string{"styles.css", "logo.svg"},
}

tether.Live(tether.LiveConfig[State]{
    Assets: []*tether.Asset{assets},
    Worker: true,
    // ...
})
```

A reconnecting indicator bar appears automatically when the connection drops and disappears when it reconnects. This works with all transport modes, regardless of the `Worker` setting.

## Wire format

Server-to-client updates are encoded by a `wire.Encoder`. The encoder
is selected at handler construction time via `LiveConfig.WireFormat`:

```go
import "github.com/jpl-au/tether/wire"

tether.Live(tether.LiveConfig[State]{
    WireFormat: wire.JSON, // default — currently the only format
    // ...
})
```

`wire.JSON` encodes updates as JSON objects. Additional formats (e.g.
HTML fragments) will be added in future. The wire format is an internal
concern — transports receive pre-encoded bytes and the client JS
handles decoding, so changing the format requires no application code
changes.

The `wire.Encoder` interface:

```go
type Encoder interface {
    Encode(u Update) ([]byte, error)
}
```

`wire.Update` carries patches, morphs, signals, and side effects in a
format-agnostic struct. The session builds a `wire.Update` after each
state change and hands it to the encoder.

## Event resilience (SSE)

Set `Client.BackgroundSync` to true to enable event queuing. When enabled, SSE events that fail to send (due to network interruptions) are queued in IndexedDB and replayed when the connection is restored.

```go
Client: tether.Client{
    BackgroundSync: true,
},
```

When the service worker is active and the browser supports Background Sync (Chromium), queued events are replayed even if the tab was closed. On other browsers, replay occurs when the tab reopens and the SSE connection restores.
