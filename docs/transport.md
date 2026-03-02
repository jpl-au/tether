# Transport

## Transport mode

Control which transports the handler accepts with the `Mode` field.
When `Mode` is not set, it defaults to `mode.Both`.

```go
// Default (mode.Both) — WebSocket with SSE fallback
tether.New(tether.Config[State]{
    Upgrade:  ws.Upgrade(),
    Fallback: sse.Upgrade(),
    // ...
})

// WebSocket only
tether.New(tether.Config[State]{
    Mode:    mode.WebSocket,
    Upgrade: ws.Upgrade(),
    // ...
})

// SSE only
tether.New(tether.Config[State]{
    Mode:     mode.ServerSentEvents,
    Fallback: sse.Upgrade(),
    // ...
})
```

Same wire format, same API regardless of transport. The encoding is
selected via `Config.WireFormat` (default `wire.JSON`).

### WebSocket options

Pass `ws.Options` to configure the WebSocket transport:

```go
ws.Upgrade(ws.Options{
    ReadLimit: 128 << 10,  // max message size (default 32 KB)
})
```

Set `ReadLimit` to match `Config.Limits.MaxEventBytes` for consistent limits across transport modes. Messages exceeding the limit cause the connection to be closed with a protocol error.

### SSE keep-alive

SSE connections send keep-alive comments at `Timeouts.Heartbeat` (default 20s) to prevent proxies from closing idle connections.

## Service worker

Enable the service worker for asset caching and offline page shells:

```go
tether.New(tether.Config[State]{
    Worker: true,
    // ...
})
```

The service worker caches the JS runtime (`fluent-tether.js`, `idiomorph.min.js`) using a cache-first strategy. Navigation responses are only cached when the server sends the `X-Tether-Cache: true` header — this prevents caching sensitive or session-specific pages without explicit intent. Cached pages are served as a fallback when offline.

To precache application assets (CSS, icons, fonts), use the `Precache` field on `Asset`:

```go
var assets = &tether.Asset{
    FS:       staticFS,
    Prefix:   "/static/",
    Precache: []string{"styles.css", "logo.svg"},
}

tether.New(tether.Config[State]{
    Assets: []*tether.Asset{assets},
    Worker: true,
    // ...
})
```

A reconnecting indicator bar appears automatically when the connection drops and disappears when it reconnects. This works with all transport modes, regardless of the `Worker` setting.

## Wire format

Server-to-client updates are encoded by a `wire.Encoder`. The encoder
is selected at handler construction time via `Config.WireFormat`:

```go
import "github.com/jpl-au/fluent-tether/wire"

tether.New(tether.Config[State]{
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
