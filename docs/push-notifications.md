# Push notifications

Reach users even when their tab is closed:

```go
import "github.com/jpl-au/fluent-poly/push"

// Generate VAPID keys once during setup (store them securely).
pub, priv, err := push.GenerateVAPIDKeys()

poly.New(poly.Config[State]{
    Push: &poly.PushConfig[State]{
        VAPIDPublicKey: pub,
        OnSubscribe: func(sess *poly.Session[State], sub poly.PushSubscription) {
            // Store sub.Endpoint and sub.Keys for later use.
        },
    },
    // ...
})

// Send a notification from anywhere.
push.Send(sub, push.Notification{
    Title:  "New message",
    Body:   "You have a new reply.",
    Tag:    "chat",     // groups related notifications
    Silent: true,       // suppress vibration and sound
    Actions: []push.NotificationAction{
        {Action: "reply", Title: "Reply", URL: "/chat?reply=1"},
        {Action: "dismiss", Title: "Dismiss"},
    },
}, push.Options{
    VAPIDPublicKey:  pub,
    VAPIDPrivateKey: priv,
    Subject:         "mailto:admin@example.com",
})
```

Subscription is never automatic — browsers require a user gesture for the push permission prompt. Use `bind.PushSubscribe` on a button or link to let the user opt in:

```go
bind.PushSubscribe(button.Text("Enable notifications"))
```

When clicked, the JS runtime requests notification permission, subscribes via the service worker's PushManager, and sends the subscription to the server via `OnSubscribe`.

The `push` subpackage implements the Web Push protocol (RFC 8291 + RFC 8292) with VAPID JWT signing and aes128gcm payload encryption. It depends on `golang.org/x/crypto` for HKDF key derivation.
