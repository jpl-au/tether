// fluent-tether-push-worker.js — minimal service worker for push only.
//
// Registered when Push is configured but Worker is false. Handles push
// events and notification clicks without intercepting fetch requests,
// caching navigation responses, or running background sync.

self.addEventListener("push", function (e) {
  var data = e.data ? e.data.json() : {};
  var title = data.title || "Notification";
  var opts = {
    body: data.body || "",
    icon: data.icon || "",
    badge: data.badge || "",
    tag: data.tag || undefined,
    renotify: !!data.renotify,
    silent: !!data.silent,
    actions: data.actions || [],
    data: { url: data.url || "/", actions: data.actions || [] }
  };
  e.waitUntil(
    self.registration.showNotification(title, opts).catch(function (err) {
      console.error("tether: showNotification failed:", err);
    })
  );
});

self.addEventListener("notificationclick", function (e) {
  e.notification.close();
  var ndata = e.notification.data || {};
  var url = ndata.url || "/";

  if (e.action && ndata.actions) {
    for (var i = 0; i < ndata.actions.length; i++) {
      if (ndata.actions[i].action === e.action && ndata.actions[i].url) {
        url = ndata.actions[i].url;
        break;
      }
    }
  }

  e.waitUntil(
    self.clients.matchAll({ type: "window" }).then(function (list) {
      var target = new URL(url, self.location.origin);
      for (var i = 0; i < list.length; i++) {
        var clientURL = new URL(list[i].url);
        if (clientURL.pathname === target.pathname && "focus" in list[i]) {
          return list[i].focus().catch(function () {
            return self.clients.openWindow(url);
          });
        }
      }
      return self.clients.openWindow(url);
    })
  );
});
