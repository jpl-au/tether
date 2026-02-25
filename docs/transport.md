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

// SSE with larger event buffer for high-frequency streams
poly.New(poly.Config[State]{
    Mode:     poly.SSEOnly,
    Fallback: sse.Upgrade(64), // default is 16
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

The service worker caches the JS runtime (`fluent-poly.js`, `idiomorph.min.js`) using a cache-first strategy, and caches page HTML using a network-first strategy. On subsequent visits, the JS loads from cache. If the server is unreachable, the last cached page is served instead of a browser error.

To precache additional app-specific assets (CSS, icons, fonts), pass their URLs to `ServeClient`:

```go
mux.Handle("/_poly/", http.StripPrefix("/_poly/", poly.ServeClient(
    "/styles.css",
    "/logo.svg",
)))
```

A reconnecting indicator bar appears automatically when the connection drops and disappears when it reconnects. This works with all transport modes, regardless of the `Worker` setting.

## Event resilience (SSE)

In SSE mode, events that fail to send (due to network interruptions) are automatically queued in IndexedDB and replayed when the connection is restored. This happens transparently — the user does not need to redo their action.

When the service worker is active and the browser supports Background Sync (Chromium), queued events are replayed even if the tab was closed. On other browsers, replay occurs when the tab reopens and the SSE connection restores.
