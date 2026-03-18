# Push notifications

Reach users even when their tab is closed:

```go
import "github.com/jpl-au/tether/push"

// Generate VAPID keys once during setup (store them securely).
pub, priv, err := push.GenerateVAPIDKeys()

sender := push.NewSender(push.Config{
    VAPIDPublicKey:  pub,
    VAPIDPrivateKey: priv,
    Subject:         "mailto:admin@example.com",
})

tether.Live(tether.LiveConfig[State]{
    Push: &tether.PushConfig[State]{
        Sender: sender,
        OnSubscribe: func(ctx context.Context, sess *tether.LiveSession[State], sub push.Subscription) {
            // Store sub.Endpoint and sub.Keys for later use.
            // Use ctx for database calls — it cancels when the session is destroyed.
        },
    },
    // ...
})

// Send via the session (uses the configured Sender automatically).
sess.Push(push.Notification{
    Title: "New message",
    Body:  "You have a new reply.",
    Tag:   "chat",     // groups related notifications
    Actions: []push.NotificationAction{
        {Action: "reply", Title: "Reply", URL: "/chat?reply=1"},
        {Action: "dismiss", Title: "Dismiss"},
    },
})

// Or send directly via the Sender with a stored subscription.
sender.Send(sub, push.Notification{
    Title: "New message",
    Body:  "You have a new reply.",
})
```

Subscription is never automatic — browsers require a user gesture for the push permission prompt. Use `bind.PushSubscribe` on a button or link to let the user opt in:

```go
bind.Apply(button.Text("Enable notifications"), bind.PushSubscribe())
```

When clicked, the JS runtime requests notification permission, subscribes via the service worker's PushManager, and sends the subscription to the server via `OnSubscribe`.

### Automatic resync on connect

The client runtime automatically re-sends the browser's existing push subscription to the server on every transport connect (including reconnects). This means subscriptions survive page reloads, navigation between pages on the same handler, and temporary disconnects — the user only needs to click the subscribe button once.

If the server's VAPID keys have changed since the subscription was created (e.g. the server restarted with new ephemeral keys), the client detects the mismatch and silently unsubscribes. The user will need to click the subscribe button again to create a fresh subscription bound to the new keys. Production apps should persist VAPID keys across restarts to avoid this.

### Notification fields

| Field | Type | Description |
|-------|------|-------------|
| `Title` | `string` | Notification title (required) |
| `Body` | `string` | Notification body text |
| `Icon` | `string` | Icon URL |
| `Badge` | `string` | Badge icon URL (small monochrome) |
| `URL` | `string` | URL to open when the notification is clicked |
| `Tag` | `string` | Groups related notifications; replaces previous with the same tag |
| `Renotify` | `bool` | Re-alert (vibration/sound) when replacing a tagged notification |
| `Silent` | `bool` | Suppress vibration and sound |
| `Actions` | `[]NotificationAction` | Up to two action buttons on the notification |

Each `NotificationAction` has `Action` (identifier), `Title` (button label), `Icon` (optional URL), and `URL` (opens when clicked; falls back to the notification's top-level URL).

### Sentinel errors

Check with `errors.Is()`:

| Error | Meaning |
|-------|---------|
| `tether.ErrPushNotConfigured` | Handler created without `PushConfig` |
| `tether.ErrPushNoSubscription` | Browser has not registered a push subscription |
| `tether.ErrPushPreWarm` | Push called during pre-warming (no browser yet) |
| `push.ErrSubscriptionExpired` | Push service returned HTTP 410 |

The `push` subpackage implements the Web Push protocol (RFC 8291 + RFC 8292) with VAPID JWT signing and aes128gcm payload encryption. It depends on `golang.org/x/crypto` for HKDF key derivation.

---

[← Back to documentation](../README.md#documentation)
