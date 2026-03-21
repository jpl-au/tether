# Reconnection Strategy

Tether uses exponential backoff with jitter for client-side reconnection when
a WebSocket or SSE transport drops. This document explains the algorithm,
its configuration, and the rationale behind the default values.

## Algorithm

When a transport connection closes unexpectedly, the client schedules a
reconnection attempt after a delay. The delay grows exponentially with each
failed attempt and is capped at a maximum value:

```
base_delay = Retry * BackoffMultiplier ^ attempt
capped_delay = min(base_delay, MaxRetry)
```

When jitter is enabled, the capped delay is multiplied by a random factor
in [0.5, 1.0) before being applied:

```
actual_delay = capped_delay * (0.5 + random() * 0.5)
```

On successful reconnection, the delay resets to the initial `Retry` value.

## Configuration

All reconnection parameters live on `tether.Timeouts`:

| Field             | Type          | Default | Description                                      |
|-------------------|---------------|---------|--------------------------------------------------|
| `Retry`           | time.Duration | 500ms   | Initial delay before the first reconnect attempt  |
| `MaxRetry`        | time.Duration | 10s     | Maximum delay between reconnect attempts          |
| `BackoffMultiplier` | float64     | 1.5     | Multiplier applied to the delay after each failure |
| `Jitter`          | *bool         | true    | Randomise each delay to prevent synchronised waves |

### Example

```go
tether.Stateful(app, tether.StatefulConfig[State]{
    Timeouts: tether.Timeouts{
        Retry:             time.Second,
        MaxRetry:          30 * time.Second,
        BackoffMultiplier: 2.0,
        Jitter:            boolPtr(false), // disable jitter
    },
    // ...
})
```

## Default delay sequence

With the defaults (500ms initial, 1.5x multiplier, 10s cap, jitter enabled),
the base delays before jitter are:

| Attempt | Base delay |
|---------|-----------|
| 1       | 500ms     |
| 2       | 750ms     |
| 3       | 1.125s    |
| 4       | 1.687s    |
| 5       | 2.531s    |
| 6       | 3.797s    |
| 7       | 5.695s    |
| 8       | 8.543s    |
| 9+      | 10s (cap) |

With jitter, each actual delay is randomly between 50% and 100% of these
values.

## Why jitter matters

When a server restarts, all connected clients lose their transport
simultaneously. Without jitter every client uses the same backoff schedule
and retries at the same instant, creating synchronised waves that can
overload the recovering server.

Jitter randomises each client's delay so reconnection attempts spread across
time. Instead of a spike at each backoff tier, the server sees a gradual
ramp. This is universally recommended by WebSocket reconnection guides and
is the approach used by HTMX, Rails ActionCable, and the websocket.org
reference implementation.

## Why these defaults

The defaults were chosen by surveying major real-time frameworks and
published best-practice guides:

| Framework / Source     | Initial delay | Backoff     | Max cap | Jitter |
|------------------------|---------------|-------------|---------|--------|
| Phoenix Channels       | 1s            | Step: 1/2/5/10s | 10s | No     |
| SignalR                | 0s            | Step: 0/2/10/30s | 30s | No    |
| Rails ActionCable      | 6-18s (random) | Exponential (< 2x) | -  | Yes   |
| HTMX ws extension      | -             | Full-jitter exponential | - | Yes |
| websocket.org guide    | 500ms         | 2x          | 30s     | Yes    |

**Initial delay (500ms):** Matches the websocket.org recommendation. Fast
enough to catch transient blips without waiting a full second.

**Multiplier (1.5):** ActionCable deliberately chose a base below 2 to keep
attempts frequent in the early window when the server is most likely to be
back. A 1.5x multiplier reaches the 10s cap in about 8 attempts rather than
4, giving more chances to catch the server as soon as it recovers.

**Max cap (10s):** Phoenix caps at 10s. For a UI framework where the user is
actively waiting, 10s keeps the experience responsive. The 30s cap used by
SignalR and websocket.org is more appropriate for infrastructure retries
where reducing server load takes priority over user-perceived latency.

**Jitter (enabled):** Universal best practice. The cost is negligible and the
benefit during multi-client reconnection scenarios is significant.
