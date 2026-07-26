# Error catalogue

Every developer-facing error and warning tether emits carries a stable
kebab-case **slug**. Client-side reports reach you two ways:

- Through `Tether.onError({ type, message, slug })` when you set that
  callback (see [operations.md](operations.md#error-reporting)).
- Otherwise on the console, where the message is followed by
  `[slug] - see tether/docs/errors.md#<slug>`. When an element is
  implicated it is passed to `console.error` as a trailing argument, so
  it is clickable and inspectable in devtools.

Find a slug below for what it means, why it fires, and how to fix it.
Server-side warnings (logged via `dev.Warn` in dev mode) and the
construction-time panics from the `bind` package are catalogued at the
end - they have no wire slug but share these stable anchors.

Slugs are stable across releases. Nothing here fires in normal
operation; every entry is an actionable signal.

---

## Client runtime

### invalid-selector

**Type:** `render`. A CSS selector built from server data, a `Flash`
target, or a `ScrollTo` target could not be parsed by
`querySelector`/`querySelectorAll` and was skipped.

Usually an unescaped special character (a period, colon, bracket, or
quote) in a selector assembled from dynamic values.

Fix: escape dynamic fragments, or prefer a signal over a selector.

```go
// Wrong - the raw ID contains a period, breaking the selector
sess.Flash("#user."+id, "Saved")

// Right - target a stable, static selector...
sess.Flash("#user-notice", "Saved")
// ...or decouple from the DOM entirely with a signal
sess.Signal("saved", true)
```

### multiple-root-elements

**Type:** `render`. A patch or morph for a Dynamic key rendered more
than one top-level element; only the first is applied. The implicated
container element is passed to the console so you can inspect it.

A `Dynamic` key must wrap exactly one root element. Two siblings at the
top of the keyed subtree trigger this.

```go
// Wrong - the key is on a fragment with two top-level elements
fragment.New(span.Text(a), span.Text(b)).Dynamic("row")

// Right - a single wrapper element carries the key
div.New(span.Text(a), span.Text(b)).Dynamic("row")
```

### invalid-computed-program

**Type:** `parse`. The JSON program behind a `bind.Computed` /
`ShowWhen` / `ClassWhen` binding on an element failed to parse. The
element is passed to the console. Almost always a framework-internal
issue or a hand-edited attribute - rebuild the binding with `bind`
rather than writing `data-tether-computed` by hand.

### computed-cycle

**Type:** `compute`. A computed signal's recomputation re-entered its
own name - a dependency cycle. The offending branch is abandoned rather
than looping.

Two computeds that read each other's output form a cycle. Push raw
inputs from the server and derive one direction only.

```go
// Wrong - total depends on subtotal and subtotal depends on total
bind.Computed("total", `subtotal + tax`)
bind.Computed("subtotal", `total - tax`)

// Right - push the inputs, derive once
sess.Signal("subtotal", 42)
sess.Signal("tax", 4)
bind.Computed("total", `subtotal + tax`)
```

### effects-island-parse

**Type:** `parse`. In HTML (stateless) mode the `<template
data-tether-effects>` island in a response body was not valid JSON, so
its side effects were dropped. The morphs still applied. The template
element is passed to the console. This points at a corrupted or
truncated response - check any proxy or middleware that rewrites HTML.

### fragment-hash-seed-parse

**Type:** `parse`. The `<template data-tether-hashes>` seed a stateless
auto-fragment page embeds could not be parsed, so the client starts with
an empty hash map (the first event may send redundant fragments, then
self-heals). The template element is passed to the console. Check
anything that post-processes the initial HTML.

### listener-threw

**Type:** `extension`. An `onUpdate`, `onSignalChange`,
`onElementAdded`, or `onElementRemoved` listener threw. The listener is
skipped; the core and other listeners are unaffected. Fix the throwing
callback - wrap risky work in your own try/catch.

### connect-ticket-failed

**Type:** `fetch` (silent). The one-time connect-ticket request before
opening a WebSocket/SSE transport failed. The client retries with
backoff. Sustained failures mean the endpoint is unreachable or a proxy
is blocking the request.

### ws-message-parse / sse-message-parse

**Type:** `parse` (silent). A frame arrived over WebSocket or SSE that
was not valid JSON and was dropped. An isolated occurrence during
reconnect is harmless; a steady stream points at a proxy corrupting the
transport or a wire-format mismatch.

### page-event-failed / event-post-failed

**Type:** `fetch`. An event POST (fetch transport) failed. `page-event-
failed` surfaces the failure; `event-post-failed` is the silent retry
path. Transient with flaky networks; persistent failures mean the
endpoint is down or CSRF/origin checks are rejecting the request - see
[security.md](security.md).

### event-queue-failed / event-replay-failed

**Type:** `indexeddb`/`fetch` (silent). With `BackgroundSync` enabled,
queuing a failed event into IndexedDB, or replaying the queue on
reconnect, failed. Private-browsing modes and storage pressure can block
IndexedDB. Events that cannot be queued are lost rather than retried.

### service-worker-registration-failed / push-worker-registration-failed

**Type:** `worker`. The service worker (offline/precache) or push worker
failed to register. Service workers require a secure context (HTTPS or
`localhost`) and a correctly served worker script. Check the script path
and its `Content-Type`.

### push-subscribe-failed / push-subscribe-post-failed / push-unsubscribe-failed

**Type:** `push`. Web Push subscription, delivering the subscription to
the server, or unsubscription failed. Common causes: the user denied
notification permission, an invalid VAPID public key, or the
subscription POST hitting an origin/CSRF rejection.

---

## Server-side warnings (dev mode)

These log through `dev.Warn` when [dev mode](operations.md) is on. They
never reach `Tether.onError`.

### memoise-no-op

`Memoise is enabled but no jit.Memoise regions were found in the
render`. `StatefulConfig.Memoise` is true but nothing in the render tree
is wrapped in `jit.Memoise`, so the flag does nothing. Either wrap the
expensive `Dynamic` regions in `jit.Memoise`, or turn `Memoise` off.

### morph-on-stateful

`Morph() called on a stateful session`. `Morph` only means something in
stateless mode - the stateful differ targets keys automatically via
`Dynamic`. Remove the call.

### state-during-handle

`State() called during Handle`. The snapshot `State()` returns reflects
state *before* the current event. Use the `state` parameter Handle
receives instead.

---

## Construction-time panics (`bind` package)

The `bind` package validates option arguments the moment you build a
binding, so a mistake fails loudly at startup rather than silently never
matching on the client. All panic with a `bind: ` prefix.

### bind-negative-duration

`Debounce`, `DebounceLeading`, `Throttle`, and `Delay` reject negative
durations. `Countdown` and `TimerPrecision` require a *positive*
duration. Pass a non-negative (or positive) `time.Duration`.

### bind-negative-length

`MinLength` and `MaxLength` reject negative values. Pass a non-negative
bound.

### bind-unknown-operator

An unknown comparison operator in `ShowWhen`/`HideWhen`/`ClassWhen`.
Valid operators are `>`, `>=`, `<`, `<=`, `==`, `!=`.

### bind-invalid-signal-name

`Computed` rejects a name that is not a valid signal name. Use a name
matching the signal grammar (letters, digits, `.`, `_`, `-`).

### bind-invalid-compute-expression

A malformed `Computed` expression - unknown operator, unbalanced
parentheses, an invalid identifier, or nesting deeper than 32 levels.
The panic message carries the position within the expression.

### bind-invalid-client-event

`OnClientEvent` accepts only a `SetSignal` or `ToggleSignal` action.
Pass one of those.

### bind-unbindable-event-name

`On` was given an event name that cannot be carried in an attribute
name. The binding renders as `data-tether-event-<name>`, and HTML
lowercases attribute names, so the name must be lowercase and contain
only letters, digits and `-` `_` `.` `:`.

Every DOM event name qualifies, as do the usual custom-event
conventions (`sl-change`, `cart:updated`). A mixed-case custom event
does not: the browser would lowercase the attribute and the two would
never match.

```go
// Wrong - the attribute would render as data-tether-event-mouseover
bind.On("mouseOver", "hover")

// Right
bind.On("mouseover", "hover")
```

To forward a genuinely mixed-case custom event, dispatch it from a
`bind.Hook` with `Tether.sendEvent`.

---

[← Back to documentation](../README.md#documentation)
