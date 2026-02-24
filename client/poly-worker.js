// poly-worker.js — service worker for Fluent Poly.
//
// Provides asset caching for faster loads, offline page shells for
// graceful disconnects, push notification handling, and background
// sync for SSE event resilience. Registered by fluent-poly.js when
// the server enables Worker mode.

var CACHE_VERSION = "poly-v1";
var PRECACHE_URLS = [
  "/_poly/fluent-poly.js",
  "/_poly/idiomorph.min.js"
];

// --- Install: precache static assets ---

self.addEventListener("install", function (e) {
  e.waitUntil(
    caches.open(CACHE_VERSION).then(function (cache) {
      return cache.addAll(PRECACHE_URLS);
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

  // Static assets (/_poly/*): cache-first. Serve from cache when
  // available, fall back to network and cache the response.
  if (url.pathname.startsWith("/_poly/") && e.request.method === "GET") {
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

  // Navigation requests: network-first with cache fallback. On
  // success, cache the page so offline refreshes show the last
  // rendered shell instead of a browser error.
  if (e.request.mode === "navigate") {
    e.respondWith(
      fetch(e.request)
        .then(function (resp) {
          var clone = resp.clone();
          caches.open(CACHE_VERSION).then(function (c) {
            c.put(e.request, clone);
          });
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
    data: { url: data.url || "/" }
  };
  e.waitUntil(self.registration.showNotification(title, opts));
});

self.addEventListener("notificationclick", function (e) {
  e.notification.close();
  var url = (e.notification.data && e.notification.data.url) || "/";
  e.waitUntil(
    self.clients.matchAll({ type: "window" }).then(function (list) {
      // Focus an existing tab if one is open at the target URL.
      for (var i = 0; i < list.length; i++) {
        if (list[i].url.indexOf(url) !== -1 && "focus" in list[i]) {
          return list[i].focus();
        }
      }
      return self.clients.openWindow(url);
    })
  );
});

// --- Background sync: replay queued SSE events ---

var EVENT_DB_NAME = "poly-events";
var EVENT_DB_VERSION = 1;
var EVENT_STORE = "queue";
// Discard events older than this to avoid replaying stale actions.
var EVENT_MAX_AGE_MS = 60000;

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
      var tx = db.transaction(EVENT_STORE, "readwrite");
      var store = tx.objectStore(EVENT_STORE);
      var req = store.getAll();
      req.onsuccess = function () {
        store.clear();
        var events = req.result;
        var now = Date.now();
        var sends = [];
        for (var i = 0; i < events.length; i++) {
          if (now - events[i].ts > EVENT_MAX_AGE_MS) continue;
          sends.push(replayEvent(events[i]));
        }
        resolve(Promise.all(sends));
      };
      req.onerror = function () { reject(req.error); };
    });
  });
}

function replayEvent(ev) {
  return fetch(ev.endpoint, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Poly-Session": ev.sessionID
    },
    body: ev.payload
  }).catch(function () {
    // Still failing — re-queue for the next sync attempt.
    return queueEventInDB(ev);
  });
}

self.addEventListener("sync", function (e) {
  if (e.tag !== "poly-event-sync") return;
  e.waitUntil(drainAndReplay());
});
