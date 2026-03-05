# Extensions

Extensions are opt-in features that add capabilities beyond the core render/handle loop. Each is activated by setting a field on `Config` — if you don't set it, there is zero overhead. Extensions work alongside `Config.Components` — component events are dispatched before Handle, but upload callbacks and push subscriptions operate at the session level independent of component routing.

## File uploads

Enable uploads by setting `Config.Upload`:

```go
tether.New(tether.Config[State]{
    Upload: &tether.UploadConfig[State]{
        Handle: func(sess *tether.LiveSession[State], upload tether.Upload) error {
            file, err := upload.Open()
            if err != nil {
                return err
            }
            defer file.Close()
            // Save to disk, S3, etc.
            // Then update the session to reflect the upload:
            sess.Update(func(s State) State {
                s.AvatarURL = savedURL
                return s
            })
            return nil
        },
        MaxSize: 5 << 20,                           // 5 MB (default 10 MB)
        Accept:  []string{"image/*", "application/pdf"}, // nil = accept all
    },
    // ...
})
```

The `Handle` callback runs in its own goroutine, so I/O operations (disk writes, S3 uploads) are safe and won't block the session loop.

### Upload struct

The `tether.Upload` passed to the Handle callback contains file metadata and a method to read the contents:

| Field | Type | Description |
|-------|------|-------------|
| `Action` | `string` | The name from `bind.Upload` (e.g. `"avatar"`) |
| `Name` | `string` | Original filename from the browser |
| `Size` | `int64` | File size in bytes |
| `ContentType` | `string` | MIME type from the multipart header |

Call `upload.Open()` to get a `multipart.File` for reading. The caller must close the returned file.

### Triggering uploads from the client

Mark an element as an upload trigger:

```go
bind.Apply(button.Text("Upload Avatar"), bind.Upload("avatar"))
```

When clicked, the JS runtime finds file inputs in the closest form or parent element and POSTs the files to the server. For file inputs, the upload fires on change.

### Progress tracking

The client sets two signals during upload:

| Signal | Values |
|--------|--------|
| `upload:{action}:progress` | `0` to `100` (percentage) |
| `upload:{action}:state` | `"idle"`, `"uploading"`, `"done"`, `"error"` |

Bind a progress bar:

```go
bind.Apply(
    progress.New().Attr("max", "100"),
    bind.UploadProgress("avatar"),
)
// Shorthand for: bind.Apply(el, bind.BindAttr("value", "upload:avatar:progress"))
```

## Service worker

Enable asset caching and offline page shells:

```go
tether.New(tether.Config[State]{
    Worker: true,
    // ...
})
```

The service worker caches the JS runtime (`fluent-tether.js`, `idiomorph.min.js`) using a cache-first strategy. Navigation responses are only cached when the server sends the `X-Tether-Cache: true` header. Cached pages are served as a fallback when offline.

### Precaching additional assets

Use the `Precache` field on `Asset` to cache app-specific assets on service worker install:

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

The cache version is derived from a content hash of all embedded files and application assets, so updates to either automatically invalidate the cache.

### Dev mode

In dev mode (`Config.DevMode` or `TETHER_DEV=1`), the service worker is not registered to ensure fresh assets during development.

## Push notifications

Push notifications are covered in detail in [push notifications](push-notifications.md). Brief setup:

```go
tether.New(tether.Config[State]{
    Push: &tether.PushConfig[State]{
        Sender: push.NewSender(push.Config{
            VAPIDPublicKey:  publicKey,
            VAPIDPrivateKey: privateKey,
            Subject:         "mailto:admin@example.com",
        }),
        OnSubscribe: func(sess *tether.LiveSession[State], sub push.Subscription) {
            // Store subscription in your database
        },
    },
    // ...
})
```

Use `bind.PushSubscribe` on a button so the user opts in with a click — browsers require a user gesture for the push permission prompt. Send notifications with:

```go
sess.Push(push.Notification{
    Title: "New message",
    Body:  "You have a new message from Alice",
    URL:   "/messages",
})
```
