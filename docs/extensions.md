# Extensions

Extensions are opt-in features that add capabilities beyond the core render/handle loop. Each is activated by setting a field on `StatefulConfig` - if you don't set it, there is zero overhead. Extensions work alongside `StatefulConfig.Components` - component events are dispatched before Handle, but upload callbacks and push subscriptions operate at the session level independent of component routing.

## File uploads

Enable uploads by setting `StatefulConfig.Upload`:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Upload: &tether.UploadConfig[State]{
        Handle: func(sess *tether.StatefulSession[State], upload tether.Upload) error {
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

## Drag and drop

Mark elements as draggable and define drop zones using bind options:

```go
// Draggable card with identifying data
bind.Apply(cardDiv,
    bind.Draggable(),
    bind.EventData("id", card.ID),
)

// Drop target receives the action with merged source + target data
bind.Apply(columnDiv,
    bind.DropTarget("card.move"),
    bind.EventData("column", "1"),
)
```

For within-container reordering, use `Sortable` instead of `DropTarget`.
The drop event includes an `"index"` key with the position:

```go
bind.Apply(todoColumn,
    bind.Sortable("card.reorder"),
    bind.EventData("column", "0"),
)

// In Handle:
case "card.reorder":
    id, _ := ev.Get("id")        // from the dragged item
    col, _ := ev.Int("column")   // from the target container
    idx, _ := ev.Int("index")    // drop position within the container
```

### How it works

The `tether-drag-and-drop.js` extension is auto-included when any element
renders `data-tether-draggable` or `data-tether-sortable`. It uses event
delegation on the tether root (consistent with the core runtime) so DOM
morphing cannot create ghost listeners.

CSS classes for visual feedback:
- `.tether-dragging` - applied to the source element during drag
- `.tether-drag-over` - applied to the drop target on hover

### Extension auto-loading

Extension scripts (`tether-upload.js`, `tether-drag-and-drop.js`) are
included in the initial page when their marker attribute appears in the
rendered HTML. If the marker first appears after a morph (e.g. a login
page transitions to a board), the runtime lazily loads the script by
inserting a `<script>` tag dynamically.

## Touch gestures

Swipe and long-press gestures for touch devices:

```go
// Swipe detection - direction in ev.Data["direction"]
bind.Apply(card, bind.OnSwipe("card.dismiss"))

// Long-press - fires after ~500ms sustained touch
bind.Apply(item, bind.OnLongPress("item.menu"))
```

The `tether-touch.js` extension is auto-included when any element
renders `data-tether-swipe` or `data-tether-longpress`. Uses event
delegation on the root. Only fires on touch devices - mouse events
are ignored.

Swipe parameters: minimum 30px distance within 500ms. Direction is
`"left"`, `"right"`, `"up"`, or `"down"`. Long-press cancels if
the finger moves more than 10px before the timeout.

## Service worker

Enable asset caching and offline page shells:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Worker: true,
    // ...
})
```

The service worker caches the JS runtime (`tether.js`, `idiomorph.min.js`) using a cache-first strategy. Navigation responses are only cached when the server sends the `X-Tether-Cache: true` header. Cached pages are served as a fallback when offline.

### Precaching additional assets

Use the `Precache` field on `Asset` to cache app-specific assets on service worker install:

```go
var assets = &tether.Asset{
    FS:       staticFS,
    Prefix:   "/static/",
    Precache: []string{"styles.css", "logo.svg"},
}

app := tether.App{
    Assets: []*tether.Asset{assets},
}

tether.Stateful(app, tether.StatefulConfig[State]{
    Worker: true,
    // ...
})
```

The cache version is derived from a content hash of all embedded files and application assets, so updates to either automatically invalidate the cache.

### Dev mode

In dev mode (`App.DevMode` or `TETHER_DEV=1`), the service worker is not registered to ensure fresh assets during development.

## Push notifications

Push notifications are covered in detail in [push notifications](push-notifications.md). Brief setup:

```go
tether.Stateful(tether.App{}, tether.StatefulConfig[State]{
    Push: &tether.PushConfig[State]{
        Sender: push.NewSender(push.Config{
            VAPIDPublicKey:  publicKey,
            VAPIDPrivateKey: privateKey,
            Subject:         "mailto:admin@example.com",
        }),
        OnSubscribe: func(ctx context.Context, sess *tether.StatefulSession[State], sub push.Subscription) {
            // Store subscription in your database.
            // Use ctx for database calls - it cancels when the session is destroyed.
        },
    },
    // ...
})
```

Use `bind.PushSubscribe` on a button so the user opts in with a click - browsers require a user gesture for the push permission prompt. Send notifications with:

```go
sess.Push(push.Notification{
    Title: "New message",
    Body:  "You have a new message from Alice",
    URL:   "/messages",
})
```

---

[← Back to documentation](../README.md#documentation)
