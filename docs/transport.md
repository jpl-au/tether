# Transport

## Transport mode

Control which transports the handler accepts with the `Mode` field:

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

Same wire format, same API regardless of transport.

SSE connections send keep-alive comments at `HeartbeatInterval` (default 20s) to prevent proxies from closing idle connections.

## Service worker

Enable the service worker for asset caching and offline page shells:

```go
poly.New(poly.Config[State]{
    Worker: true,
    // ...
})
```

The service worker caches the JS runtime (`fluent-poly.js`, `idiomorph.min.js`) using a cache-first strategy. Navigation responses are only cached when the server sends the `X-Poly-Cache: true` header — this prevents caching sensitive or session-specific pages without explicit intent. Cached pages are served as a fallback when offline.

To precache application assets (CSS, icons, fonts), use the `Precache` field on `Asset`:

```go
var assets = &poly.Asset{
    FS:       staticFS,
    Prefix:   "/static/",
    Precache: []string{"styles.css", "logo.svg"},
}

poly.New(poly.Config[State]{
    Assets: []*poly.Asset{assets},
    Worker: true,
    // ...
})
```

A reconnecting indicator bar appears automatically when the connection drops and disappears when it reconnects. This works with all transport modes, regardless of the `Worker` setting.

## Event resilience (SSE)

Set `Client.BackgroundSync` to true to enable event queuing. When enabled, SSE events that fail to send (due to network interruptions) are queued in IndexedDB and replayed when the connection is restored.

```go
Client: poly.Client{
    BackgroundSync: true,
},
```

When the service worker is active and the browser supports Background Sync (Chromium), queued events are replayed even if the tab was closed. On other browsers, replay occurs when the tab reopens and the SSE connection restores.
