# Security

## Security model

Tether secures the transport layer: session integrity, cross-origin
protection, and safe HTML delivery. Application-level concerns -
authentication, authorisation, per-IP rate limiting, and Content Security
Policy - are intentionally left to the developer.

This is a deliberate design choice. These concerns are highly
application-specific, and a framework that bakes in opinionated defaults
would either be too restrictive for real applications or too permissive
to be meaningful. Tether provides the primitives (middleware, capacity
limits, configuration hooks) so you can implement policies that fit your
deployment.

The sections below describe what Tether provides and where its
responsibility ends.

## TLS is required

Session IDs are bearer tokens - knowing one is sufficient to send events,
reconnect, upload files, and register push subscriptions. They travel in
WebSocket upgrade URLs, POST headers, and HTML attributes. Without TLS, any
network observer can intercept them.

**Always deploy behind TLS in production.** Use a reverse proxy (nginx,
Caddy, Cloudflare) or `Handler.ListenAndServeTLS` directly.

## Session identity

Each session is identified by a cryptographically random ID generated with
`crypto/rand.Text` (128-bit entropy). The ID is the sole proof of ownership
 -  there is no secondary authentication, no cookie, and no HMAC.

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

#### Custom matching

By default, the User-Agent must match exactly. For deployments where
browser auto-updates change the UA version during long-lived frozen
sessions, provide a custom `SessionMatch` function:

```go
app := tether.App{
    Security: tether.Security{
        SessionMatch: func(original, reconnect string) bool {
            // Accept any UA with the same browser family prefix.
            return extractBrowser(original) == extractBrowser(reconnect)
        },
    },
}
```

The framework does not parse User-Agent strings itself. The developer
provides the matching logic suited to their deployment.

#### Disabling entirely

To turn off binding completely (e.g. trusted internal environments):

```go
app := tether.App{
    Security: tether.Security{
        DisableSessionBinding: true,
    },
}
```

When disabled, `SessionMatch` is ignored and any client can reconnect
to any session.

### Where the ID appears

| Location | Purpose |
|----------|---------|
| `data-tether-session` HTML attribute | Client reclaims session on transport connect |
| `Tether-Session` POST header | Events, uploads, push subscriptions, connect tickets |
| Destroy beacon POST body | Immediate teardown on page unload |
| Server logs (debug level) | Correlating session activity |

### Why the session ID is never in a URL

URLs are captured by web-server access logs, reverse proxies, and APM
traces, so a bearer token in a query string leaks into infrastructure
the application does not control. Browsers do not allow custom headers
on `new WebSocket()` or `new EventSource()` calls, so the transport
connect cannot carry the ID in a header directly. Instead the client
first POSTs to the endpoint with `?tether=ticket`, sending the session
ID in the `Tether-Session` header (and any replaced session in
`Tether-Replaces`). The server answers with an opaque, single-use
connect ticket that expires in seconds, and the transport connects
with `?ticket=<token>`. A logged copy of the ticket is worthless: it
dies on first use, expires almost immediately, and is bound to the
issuing client's User-Agent.

Session binding adds a second layer - even a live session ID is not
sufficient to reconnect; the client must also present the original
User-Agent (a tripwire against casual replay, not real protection
against a capable attacker).

### Mitigations

- **Session binding** - User-Agent verification on reconnect (enabled by
  default).
- `Referrer-Policy: same-origin` prevents leakage via the Referer header on
  external navigation.
- Custom headers (`Tether-Session`) on POST requests trigger CORS
  preflight, preventing cross-origin abuse from browsers.
- Session IDs are never included in error responses sent to clients.

### Recommendations

- **Treat session IDs as credentials** in log storage and rotation policies.
- **Use TLS** so IDs cannot be sniffed in transit.
- **Consider Content-Security-Policy** (`script-src 'self'`) as
  defence-in-depth - even if XSS occurs, inline scripts are blocked, making
  it harder to exfiltrate session IDs from the DOM.

## Cross-origin protection

The handler defends against two distinct cross-origin threats:

**CSRF on POST requests** - Go 1.25's `http.CrossOriginProtection`
checks `Sec-Fetch-Site` and `Origin` headers on all state-changing
methods (POST events, uploads, push subscriptions). Safe methods
(GET, HEAD) are always allowed - this includes the initial page
render and SSE streams.

**Cross-site WebSocket hijacking** - WebSocket upgrades are GET
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
`Sec-Fetch-Site` headers entirely - that is by design, since
same-origin navigations and non-browser clients (curl,
server-to-server) legitimately omit them.

Requests with no Origin header are allowed. The security boundary for
non-browser attackers is the session ID itself (128-bit random, requiring
TLS).

## CSRF protection

The framework uses a layered approach that does not rely on cookies:

1. **Sec-Fetch-Site + Origin validation** on state-changing requests
   (POST events, WebSocket upgrades, uploads, push subscriptions).
2. **Custom headers** (`Tether-Session`, `Tether-Upload`,
   `Tether-Push-Subscribe`) on all POST requests - these trigger
   CORS preflight, which browsers enforce.
3. **No cookies** - the framework does not set or read cookies,
   eliminating cookie-based CSRF vectors entirely.

The custom-header approach is stronger than traditional CSRF tokens because
it cannot be bypassed by token leakage - the browser's CORS preflight is the
enforcement mechanism.

## Rate limiting

The framework provides capacity limits (`MaxSessions`, `MaxPending`,
`MaxEventBytes`) but does not implement per-IP rate limiting. This is
intentionally left to the deployment layer:

- **Reverse proxy** - nginx `limit_req`, HAProxy `stick-table`, Cloudflare
  rate limiting
- **Middleware** - Go rate-limiting middleware in front of the handler

### Capacity defaults

`MaxSessions` and `MaxPending` are on `App` (server-wide).
`MaxEventBytes` and `MaxPushSubscriptionBytes` are on
`StatefulConfig.Limits` (per-handler).

| Limit | Default | Purpose |
|-------|---------|---------|
| `App.MaxSessions` | 0 (unlimited) | Total concurrent sessions - **set this in production** |
| `App.MaxPending` | 128 | Pre-warmed sessions awaiting transport connection |
| `Limits.MaxEventBytes` | 64 KB | POST event body size |
| `Limits.MaxPushSubscriptionBytes` | 4 KB | Push subscription body size |

`MaxPending` protects against GET-flooding where an attacker scripts
thousands of requests without connecting. `MaxSessions` caps total resource
consumption. A startup warning is emitted when `MaxSessions` is zero.

## IndexedDB event storage

When `Client.BackgroundSync` is enabled, failed SSE POST events are stored
in IndexedDB for replay on reconnect. Stored records include the session ID,
endpoint, payload, and timestamp.

### Cleanup

- **Age-based expiry** - events older than `Client.SyncRetention` (default
  1 hour) are discarded during replay.
- **Orphan cleanup** - events from previous sessions are deleted when the
  current session replays its queue.
- **Permanent failure cleanup** - events that receive a 4xx response
  (session not found, bad request) are deleted immediately rather than
  retried.
- **Service worker** - applies the same age-based expiry and 4xx cleanup
  via the Background Sync API.

IndexedDB is per-origin, so only scripts on the same origin can access
stored events. If XSS is a concern, consider a strict
`Content-Security-Policy` to limit script execution.

## HTML escaping

The wire protocol sends pre-rendered HTML patches which the client applies
via `innerHTML`. This is safe because [Fluent](https://github.com/jpl-au/fluent)
escapes untrusted values by default, in two places:

- **Text content** - `Text()` and `Textf()` HTML-escape their input.
  `RawText()`/`RawTextf()` deliberately do not, for content you have
  already sanitised.
- **Attribute values** - every attribute set through a generated setter
  (`Class`, `Href`, `SetAttribute`, `SetData`, ...) is HTML-escaped at set
  time, and URL sinks (`href`, `src`, `action`, ...) are scheme-filtered to
  an `http`/`https`/`mailto`/`tel`/`sms`/relative allowlist (an off-allowlist
  scheme becomes the inert `node.UnsafeURL` sentinel). `SetAttributeRaw()`
  stores a trusted value verbatim, without escaping.

The transport layer deliberately does not double-escape.

The trust chain:

```
User state → Fluent render (escapes text + attributes) → HTML string → JSON patch → innerHTML
```

A failure would come from bypassing Fluent's escaping - inserting raw user
input via `RawText()`/`RawTextf()` or `SetAttributeRaw()` without sanitising
it first. Fluent is the single point of responsibility for escaping; audit
any use of those raw hatches.

For untrusted *HTML markup* - user-provided HTML fragments, rendered
markdown, rich-text input - reach for the
[fluent-security](https://github.com/jpl-au/fluent-security) module. It
wraps [bluemonday](https://github.com/microcosm-cc/bluemonday) and returns
Fluent nodes directly: `security.HTML(input)` and `security.PlainText(input)`
for the common cases, or the chainable `Cleaner` (`New()`, `RichText()`,
`FromPolicy()`, `.Clean()`) for a custom policy.

## Developer responsibilities

The following concerns are outside Tether's scope. This is intentional -
they depend on your application's requirements and deployment
environment.

### Authentication and authorisation

Tether has no concept of users, roles, or permissions. Sessions are
anonymous transport channels. To restrict access:

- Use **middleware** to check credentials before the handler runs.
- Use the **`InitialState` callback** to scope session state to an
  authenticated user.
- Implement **authorisation checks** in your event handlers.

### Per-IP rate limiting

The capacity limits (`MaxSessions`, `MaxPending`, `MaxEventBytes`)
protect against resource exhaustion but do not throttle individual
clients. For per-IP rate limiting, use your reverse proxy (nginx
`limit_req`, Cloudflare rate rules) or Go rate-limiting middleware in
front of the handler.

### Content Security Policy

Tether does not set CSP headers. CSP policies are application-specific -
they depend on which external resources your application loads, whether
you use inline scripts or styles, and your tolerance for restrictiveness.

Add CSP headers via your reverse proxy or a middleware wrapper. A
`script-src 'self'` policy is recommended as defence-in-depth against
XSS and session ID exfiltration from the DOM.

### Server patches never execute scripts

The client applies server updates by morphing HTML into the DOM with
idiomorph. `<script>` elements inserted this way do **not** run - the
runtime never re-creates them to force execution, and there is no
`eval`, `new Function`, or inline-handler evaluation anywhere in the
client. A server patch can therefore change markup but can never inject
executable JavaScript into the page.

This is a deliberate security posture, not an oversight. All client-side
behaviour is declarative (`data-tether-*` attributes) and all runtime
JavaScript ships as framework-controlled extension scripts loaded by
`src` from `/_tether/`. Because nothing in a patch is evaluated, a strict
`script-src 'self'` CSP holds even under a full stream of server updates,
and a compromised or malicious render cannot escalate to script
execution. The trade-off is that application code cannot smuggle inline
`<script>` through a render; load behaviour as an extension or a lifecycle
`bind.Hook` instead.

Computed signals (`bind.Computed`, and the `ShowWhen`/`HideWhen`/`ClassWhen`
conditional bindings) uphold this guarantee. The infix expression is
compiled to a compact postfix program **in Go, at construction time**;
the client ships a fixed stack-machine interpreter over a closed opcode
set and never evaluates a string. There is no `eval` and no `new Function`
in the compute path - an expression cannot even name a function, because
the grammar has no call syntax - so a `script-src 'self'` CSP holds with
computed signals in play exactly as it does without them.

### Speculation Rules (prefetch)

The [`Prefetch` effect](server-updates.md#prefetch) appends a
`<script type="speculationrules">` element to the head. Despite the tag
name, this is **declarative browser data, not executable code**: the
client builds it with `createElement` + `textContent` (never `innerHTML`),
the browser parses the body as JSON, and nothing in it is evaluated.
Tether still needs no `unsafe-inline` and no `unsafe-eval`.

Speculation-rules scripts are the one case a strict `script-src`
deployment must acknowledge, because the browser treats them as inline
script for CSP purposes. Add the dedicated keyword - not `unsafe-inline`:

```
Content-Security-Policy: script-src 'self' 'inline-speculation-rules'
```

`'inline-speculation-rules'` permits *only* `<script type="speculationrules">`
and nothing else, so the `script-src 'self'` posture above is preserved.
Deployments that do not use `Prefetch`, or that are content with the
`<link rel="prefetch">` fallback (emitted automatically where the
Speculation Rules API is unavailable), need no change at all.

### Summary

| Concern | Tether provides | Developer responsibility |
|---------|----------------|------------------------|
| Session identity | 128-bit crypto random IDs | Treat session IDs as credentials in logs |
| Session binding | User-Agent verification on reconnect (default on) | - |
| CSRF | Sec-Fetch-Site + Origin + custom headers, no cookies | Configure `TrustedOrigins` for production |
| WebSocket origin | Two-layer check (Sec-Fetch-Site then Origin) | Configure `TrustedOrigins` |
| TLS | - | Deploy behind TLS |
| Capacity limits | `App.MaxSessions`, `App.MaxPending`, `Limits.MaxEventBytes` | Set `App.MaxSessions` in production |
| Per-IP rate limiting | - | Reverse proxy or Go middleware |
| HTML escaping | Delegated to Fluent's `Text()` and attribute setters (auto-escape) | Audit any use of `RawText` / `SetAttributeRaw` |
| Content sanitisation | - | Use the [fluent-security](https://github.com/jpl-au/fluent-security) module for untrusted HTML markup |
| Script execution in patches | Never executed - no `eval`/`Function`, scripts in morphed HTML do not run | - |
| Prefetch hints | Declarative `speculationrules` data, no `eval`/`innerHTML` | Add `'inline-speculation-rules'` to `script-src` if using `Prefetch` under strict CSP |
| CSP headers | - | Add via reverse proxy or middleware |
| Authentication | - | Implement via middleware or `InitialState` |
| Authorisation | - | Implement via middleware or event handlers |

---

[← Back to documentation](../README.md#documentation)
