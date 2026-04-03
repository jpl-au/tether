# Utilities

## Router

Dispatch `Render` and `Handle` by URL path:

```go
r := router.New[State](func(s State) string { return s.Page })
r.Route("/", router.Page[State]{Render: homeRender, Handle: homeHandle})
r.Route("/settings", router.Page[State]{Render: settingsRender})
r.NotFound(router.Page[State]{Render: notFoundRender})

tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Render:       r.Render,
    Handle:       r.Handle,
    OnNavigate: r.OnNavigate(func(s *State, p tether.Params) { s.Page = p.Path }),
})
```

---

## Assets

Two modes of operation:

**Embedded** (single binary, immutable):
```go
//go:embed static
var staticFS embed.FS

var assets = &tether.Asset{
    FS:     staticFS,
    Prefix: "/static/",
}
```

**Filesystem** (external, watched for changes):
```go
var assets = &tether.Asset{
    FS:       os.DirFS("./static"),
    Prefix:   "/static/",
    WatchDir: "./static",
}
```

When `WatchDir` is set, the asset manager uses `fsnotify` to watch the
directory. When a file changes, only that file's hash is recomputed.
Subsequent requests get the new hash in the URL, so browsers fetch the
updated asset. Call `assets.Close()` on shutdown to stop the watcher.

| Field | Type | Description |
|-------|------|-------------|
| `FS` | `fs.FS` | Asset filesystem. `embed.FS` or `os.DirFS`. Required |
| `Prefix` | `string` | URL path prefix (must end with `/`). Default `/assets/` |
| `WatchDir` | `string` | Filesystem path to watch. Empty disables watching |
| `Precache` | `[]string` | Asset paths for service worker pre-caching |

---

## Middleware

Wraps `Handle` for cross-cutting concerns. Applied outermost-first:

```go
type Middleware[S any] func(HandleFunc[S]) HandleFunc[S]

tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Middleware: []tether.Middleware[State]{withLogging, withAuth},
})
```

---

## Catch

Render-level error boundary:

```go
tether.Catch(func() node.Node {
    return riskyWidget(s)
}, span.Text("Unavailable"))
```

Recovers panics, logs them, and returns the fallback node.

---

## Push

Web Push notifications via the `push` package:

```go
sender := push.NewSender(push.Config{
    VAPIDPublicKey:  pub,
    VAPIDPrivateKey: priv,
    Subject:         "mailto:admin@example.com",
})

sess.Push(push.Notification{
    Title: "New message",
    Body:  "From Alice",
    URL:   "/messages",
})

// Generate new VAPID keys
pub, priv, err := push.GenerateVAPIDKeys()
```

**Sentinel errors** - check with `errors.Is()`:

| Error | Meaning |
|-------|---------|
| `tether.ErrPushNotConfigured` | Handler created without `PushConfig` |
| `tether.ErrPushNoSubscription` | Browser has not registered a push subscription |
| `tether.ErrPushPreWarm` | Push called during pre-warming (no browser yet) |
| `push.ErrSubscriptionExpired` | Push service returned HTTP 410 |

---

## Window

Virtual scrolling helper for large lists. See the [windowing guide](windowing.md) for full usage.

```go
import "github.com/jpl-au/tether/window"

window.New(window.Config{
    Total:     len(s.Items),
    Offset:    s.ScrollOffset,
    PageSize:  30,
    RowHeight: 40,
    Row:       func(i int) node.Node { return renderRow(s.Items[i]) },
})
```

| `Config` field | Type | Description |
|----------------|------|-------------|
| `Total` | `int` | Full dataset size |
| `Offset` | `int` | First visible item index |
| `PageSize` | `int` | Items to render (viewport + buffer) |
| `RowHeight` | `int` | Uniform row height in pixels |
| `Row` | `func(int) node.Node` | Renders a single item by index |

---

[← API reference](api.md)
