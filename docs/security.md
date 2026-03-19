# Security

## TLS is required

Session IDs are bearer tokens — knowing one is sufficient to send events,
reconnect, upload files, and register push subscriptions. They travel in
WebSocket upgrade URLs, POST headers, and HTML attributes. Without TLS, any
network observer can intercept them.

**Always deploy behind TLS in production.** Use a reverse proxy (nginx,
Caddy, Cloudflare) or `Handler.ListenAndServeTLS` directly.

## Session identity

Each session is identified by a cryptographically random ID generated with
`crypto/rand.Text` (128-bit entropy). The ID is the sole proof of ownership
— there is no secondary authentication, no cookie, and no HMAC.

### Session binding

The framework verifies the `User-Agent` header on session reconnect by
default. When a session is created, the client's User-Agent is captured.
On every subsequent reconnect or session claim, the User-Agent must match
the original. A mismatch rejects the connection and emits a
`SessionBindingFailed` diagnostic.

This detects stolen session IDs presented from a different client. It does
not prevent spoofing by an attacker who also knows the User-Agent string,
but it raises the bar for casual attacks and adds a layer of defence
alongside TLS and origin checking.

To disable (e.g. for environments where User-Agents change mid-session):

```go
app := tether.App{
    Security: tether.Security{
        DisableSessionBinding: true,
    },
}

tether.Stateful(app, tether.StatefulConfig[State]{
    // ...
})
```

### Where the ID appears

| Location | Purpose |
|----------|---------|
| `data-tether-session` HTML attribute | Client reclaims session on transport connect |
| WebSocket upgrade URL (`?session=ID`) | Server looks up the session to attach |
| `X-Tether-Session` POST header | SSE mode event delivery |
| Server logs (debug level) | Correlating session activity |

### Why the session ID is in the URL

Browsers do not allow custom headers on `new WebSocket()` calls. The only
alternatives are cookies (which the framework deliberately avoids to
eliminate cookie-based CSRF) or the WebSocket sub-protocol field (a misuse
of the spec). A query parameter is the standard approach used by Phoenix
LiveView, Laravel Livewire, and similar frameworks.

Session binding mitigates the risk of URL exposure — knowing the session ID
alone is not sufficient to hijack a session; the attacker must also present
the correct User-Agent.

### Mitigations

- **Session binding** — User-Agent verification on reconnect (enabled by
  default).
- `Referrer-Policy: same-origin` prevents leakage via the Referer header on
  external navigation.
- Custom headers (`X-Tether-Session`) on POST requests trigger CORS
  preflight, preventing cross-origin abuse from browsers.
- Session IDs are never included in error responses sent to clients.

### Recommendations

- **Treat session IDs as credentials** in log storage and rotation policies.
- **Use TLS** so IDs cannot be sniffed in transit.
- **Consider Content-Security-Policy** (`script-src 'self'`) as
  defence-in-depth — even if XSS occurs, inline scripts are blocked, making
  it harder to exfiltrate session IDs from the DOM.

## Cross-origin protection

The handler defends against two distinct cross-origin threats:

**CSRF on POST requests** — Go 1.25's `http.CrossOriginProtection`
checks `Sec-Fetch-Site` and `Origin` headers on all state-changing
methods (POST events, uploads, push subscriptions). Safe methods
(GET, HEAD) are always allowed — this includes the initial page
render and SSE streams.

**Cross-site WebSocket hijacking** — WebSocket upgrades are GET
requests that become bidirectional, so the stdlib's safe-method
exemption does not apply. The handler checks `Sec-Fetch-Site` first
(available in all browsers since 2023), then falls back to `Origin`
header comparison against `TrustedOrigins` or the `Host` header.

Configure trusted origins explicitly for production:

```go
app := tether.App{
    Security: tether.Security{
        TrustedOrigins: []string{
            "https://example.com",
            "https://staging.example.com",
        },
    },
}

tether.Stateful(app, tether.StatefulConfig[State]{
    // ...
})
```

When `TrustedOrigins` is empty, the handler falls back to same-host
checking (the Origin header's host:port must match the request's Host
header exactly). This is suitable for development but should be
replaced with an explicit list in production.

### Trust model

Cross-origin protection defends against **browser-based cross-origin
attacks** (CSRF, cross-site WebSocket hijacking). It does not protect
against non-browser attackers who omit the `Origin` and
`Sec-Fetch-Site` headers entirely — that is by design, since
same-origin navigations and non-browser clients (curl,
server-to-server) legitimately omit them.

Requests with no Origin header are allowed. The security boundary for
non-browser attackers is the session ID itself (128-bit random, requiring
TLS).

## CSRF protection

The framework uses a layered approach that does not rely on cookies:

1. **Sec-Fetch-Site + Origin validation** on state-changing requests
   (POST events, WebSocket upgrades, uploads, push subscriptions).
2. **Custom headers** (`X-Tether-Session`, `X-Tether-Upload`,
   `X-Tether-Push-Subscribe`) on all POST requests — these trigger
   CORS preflight, which browsers enforce.
3. **No cookies** — the framework does not set or read cookies,
   eliminating cookie-based CSRF vectors entirely.

The custom-header approach is stronger than traditional CSRF tokens because
it cannot be bypassed by token leakage — the browser's CORS preflight is the
enforcement mechanism.

## Rate limiting

The framework provides capacity limits (`MaxSessions`, `MaxPending`,
`MaxEventBytes`) but does not implement per-IP rate limiting. This is
intentionally left to the deployment layer:

- **Reverse proxy** — nginx `limit_req`, HAProxy `stick-table`, Cloudflare
  rate limiting
- **Middleware** — Go rate-limiting middleware in front of the handler

### Capacity defaults

| Limit | Default | Purpose |
|-------|---------|---------|
| `MaxSessions` | 0 (unlimited) | Total concurrent sessions — **set this in production** |
| `MaxPending` | 128 | Pre-warmed sessions awaiting transport connection |
| `MaxEventBytes` | 64 KB | POST event body size |
| `MaxPushSubscriptionBytes` | 4 KB | Push subscription body size |

`MaxPending` protects against GET-flooding where an attacker scripts
thousands of requests without connecting. `MaxSessions` caps total resource
consumption. A startup warning is emitted when `MaxSessions` is zero.

## IndexedDB event storage

When `Client.BackgroundSync` is enabled, failed SSE POST events are stored
in IndexedDB for replay on reconnect. Stored records include the session ID,
endpoint, payload, and timestamp.

### Cleanup

- **Age-based expiry** — events older than `Client.SyncRetention` (default
  1 hour) are discarded during replay.
- **Orphan cleanup** — events from previous sessions are deleted when the
  current session replays its queue.
- **Permanent failure cleanup** — events that receive a 4xx response
  (session not found, bad request) are deleted immediately rather than
  retried.
- **Service worker** — applies the same age-based expiry and 4xx cleanup
  via the Background Sync API.

IndexedDB is per-origin, so only scripts on the same origin can access
stored events. If XSS is a concern, consider a strict
`Content-Security-Policy` to limit script execution.

## HTML escaping

The wire protocol sends pre-rendered HTML patches which the client applies
via `innerHTML`. This is safe because the [fluent](https://github.com/jpl-au/fluent)
template engine escapes all text content by default. The transport layer
deliberately does not double-escape.

The trust chain:

```
User state → fluent render (escapes) → HTML string → JSON patch → innerHTML
```

A failure at the fluent render step (e.g. raw user input inserted via
`UnsafeRaw` without escaping) would result in XSS. The fluent engine is
the single point of responsibility for escaping — audit it if you use
`UnsafeRaw` or similar bypass functions.

---

[← Back to documentation](../README.md#documentation)
