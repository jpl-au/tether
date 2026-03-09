// fluent-tether-worker.js — service worker for Fluent Tether.
//
// Provides asset caching for faster loads, offline page shells for
// graceful disconnects, push notification handling, and background
// sync for SSE event resilience. Registered by fluent-tether.js when
// the server enables Worker mode.

var CACHE_VERSION = "tether-v1";
var PRECACHE_URLS = [
  "/_tether/fluent-tether.js",
  "/_tether/idiomorph.min.js"
];
var PRECACHE_EXTRA = [];

// --- Install: precache static assets ---

self.addEventListener("install", function (e) {
  e.waitUntil(
    caches.open(CACHE_VERSION).then(function (cache) {
      return cache.addAll(PRECACHE_URLS.concat(PRECACHE_EXTRA));
    })
  );
  self.skipWaiting();
});

// --- Activate: clean old caches ---

self.addEventListener("activate", function (e) {
  e.waitUntil(
    caches.keys().then(function (keys) {
      return Promise.all(
        keys
          .filter(function (k) { return k !== CACHE_VERSION; })
          .map(function (k) { return caches.delete(k); })
      );
    })
  );
  self.clients.claim();
});

// --- Fetch: serve cached assets and page shells ---

self.addEventListener("fetch", function (e) {
  var url = new URL(e.request.url);

  // Static assets (/_tether/*): cache-first. Serve from cache when
  // available, fall back to network and cache the response.
  if (url.pathname.startsWith("/_tether/") && e.request.method === "GET") {
    e.respondWith(
      caches.match(e.request).then(function (cached) {
        if (cached) return cached;
        return fetch(e.request).then(function (resp) {
          var clone = resp.clone();
          caches.open(CACHE_VERSION).then(function (c) {
            c.put(e.request, clone);
          });
          return resp;
        });
      })
    );
    return;
  }

  // Navigation requests: pass through to the network by default.
  // Only cache the response when the server opts in with the
  // X-Tether-Cache header — this prevents caching sensitive or
  // session-specific pages without explicit intent.
  if (e.request.mode === "navigate") {
    e.respondWith(
      fetch(e.request)
        .then(function (resp) {
          if (resp.headers.get("X-Tether-Cache") === "true") {
            var clone = resp.clone();
            caches.open(CACHE_VERSION).then(function (c) {
              c.put(e.request, clone);
            });
          }
          return resp;
        })
        .catch(function () {
          return caches.match(e.request);
        })
    );
    return;
  }

  // All other requests (WebSocket, SSE, POST events) pass through
  // to the network unchanged.
});

// --- Push notifications ---

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

  // When the user clicks an action button, find its URL.
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
      // Focus an existing tab whose pathname matches the target URL.
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

// --- Background sync: replay queued SSE events ---

var EVENT_DB_NAME = "tether-events";
var EVENT_DB_VERSION = 1;
var EVENT_STORE = "queue";
// Discard events older than this to avoid replaying stale actions.
// Read from the registration URL query string so the server's
// Client.SyncRetention config is respected. Falls back to 1 hour.
var EVENT_MAX_AGE_MS = (function () {
  try {
    var params = new URL(self.location).searchParams;
    return parseInt(params.get("syncRetention")) || 3600000;
  } catch (e) {
    return 3600000;
  }
})();

function openEventDB() {
  return new Promise(function (resolve, reject) {
    var req = indexedDB.open(EVENT_DB_NAME, EVENT_DB_VERSION);
    req.onupgradeneeded = function (e) {
      e.target.result.createObjectStore(EVENT_STORE, { autoIncrement: true });
    };
    req.onsuccess = function (e) { resolve(e.target.result); };
    req.onerror = function (e) { reject(e.target.error); };
  });
}

function queueEventInDB(ev) {
  return openEventDB().then(function (db) {
    return new Promise(function (resolve, reject) {
      var tx = db.transaction(EVENT_STORE, "readwrite");
      tx.objectStore(EVENT_STORE).add(ev);
      tx.oncomplete = function () { resolve(); };
      tx.onerror = function () { reject(tx.error); };
    });
  });
}

function drainAndReplay() {
  return openEventDB().then(function (db) {
    return new Promise(function (resolve, reject) {
      var tx = db.transaction(EVENT_STORE, "readonly");
      var store = tx.objectStore(EVENT_STORE);
      var allReq = store.getAll();
      var keysReq = store.getAllKeys();
      tx.oncomplete = function () {
        var events = allReq.result;
        var keys = keysReq.result;
        var now = Date.now();
        var sends = [];
        for (var i = 0; i < events.length; i++) {
          if (now - events[i].ts > EVENT_MAX_AGE_MS) {
            deleteFromEventDB(db, keys[i]);
            continue;
          }
          sends.push(replayEvent(db, keys[i], events[i]));
        }
        resolve(Promise.all(sends));
      };
      tx.onerror = function () { reject(tx.error); };
    });
  });
}

function replayEvent(db, key, ev) {
  return fetch(ev.endpoint, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Tether-Session": ev.sessionID
    },
    body: ev.payload
  }).then(function (resp) {
    // Delete on success or permanent client error (4xx — e.g. session
    // not found). Keep on server error (5xx) for retry on next sync.
    if (resp.ok || (resp.status >= 400 && resp.status < 500)) {
      deleteFromEventDB(db, key);
    }
  }).catch(function () {
    // Network failure — leave in IndexedDB for the next sync attempt.
  });
}

function deleteFromEventDB(db, key) {
  var tx = db.transaction(EVENT_STORE, "readwrite");
  tx.objectStore(EVENT_STORE).delete(key);
}

self.addEventListener("sync", function (e) {
  if (e.tag !== "tether-event-sync") return;
  e.waitUntil(drainAndReplay());
});
