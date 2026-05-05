// tether.js - client runtime for Tether reactive UI.
//
// This script is injected automatically by the tether handler. It connects
// to the server via WebSocket, applies patches to the DOM using idiomorph,
// and sends user events back to the server. The developer never imports
// or configures this file directly.
//
// On DOMContentLoaded it finds [data-tether-root], reads the endpoint from
// data-tether-endpoint, opens a WebSocket, binds event delegation, and
// starts applying patches.

// Tether.hooks is the public API for JS interop. Developers register
// named hooks with mounted/updated/destroyed callbacks:
//
//   Tether.hooks.chart = {
//     mounted: function(el) { /* init chart library */ },
//     updated: function(el) { /* refresh chart */ },
//     destroyed: function(el) { /* teardown */ }
//   };
// Tether.onError is an optional callback for client-side error reporting.
// When set, it receives an object with {type, message} for every error
// or warning the runtime encounters. Types: "parse", "fetch", "worker",
// "push", "indexeddb", "render". If not set, warnings are logged to
// the console and silent errors remain silent.
window.Tether = window.Tether || {};
window.Tether.hooks = window.Tether.hooks || {};
window.Tether.signals = window.Tether.signals || {};

// Tether.decode parses a server message from its wire representation.
// The default is JSON.parse. Wire format extensions (e.g. tether-wire-cbor.js)
// override this to decode CBOR or other binary formats.
window.Tether.decode = window.Tether.decode || JSON.parse;

(function () {
  "use strict";

  var root = null;
  var endpoint = "";
  var sessionID = "";
  var ws = null;
  var retryDelay = 0;
  var initialRetryDelay = 0;
  var maxRetryDelay = 0;
  var backoffMultiplier = 1.5;
  var jitter = true;
  var defaultDebounce = 0;
  var transitionTimeout = 0;
  var debounceTimers = {};
  var leavingNodes = new Set();
  var pendingElements = {};
  var eventCounter = 0;
  var transportMode = "ws"; // "ws", "sse", or "auto" - set from data-tether-transport
  var connectionMode = "ws";
  var eventSource = null;
  var wsOpened = false;
  var sseOpened = false;
  var devMode = false;
  var backgroundSync = false;
  var syncRetention = 3600000; // 1 hour default
  var flashDuration = 5000;
  var toastDuration = 5000;
  var pendingCount = 0;
  var eventDataPrefix = "data-tether-data-";

  // Report an error or warning to the Tether.onError callback if set.
  // Falls back to console.warn for non-silent errors.
  function reportError(type, message, silent) {
    if (typeof window.Tether.onError === "function") {
      window.Tether.onError({ type: type, message: message });
    } else if (!silent) {
      console.warn("tether: " + message);
    }
  }

  // Safe querySelector wrappers. Dynamic values interpolated into CSS
  // selectors can contain special characters (periods, colons, brackets,
  // quotes) that cause SyntaxError. These helpers catch the error and
  // report it via the standard error channel instead of crashing the
  // caller. Use these for any selector built from server data, user
  // input, or DOM attribute values.

  function safeQuery(parent, selector) {
    try {
      return parent.querySelector(selector);
    } catch (e) {
      reportError("render", "invalid selector: " + selector);
      return null;
    }
  }

  function safeQueryAll(parent, selector) {
    try {
      return parent.querySelectorAll(selector);
    } catch (e) {
      reportError("render", "invalid selector: " + selector);
      return [];
    }
  }

  // Build an attribute selector with the value properly escaped.
  // Example: attrSelector("data-tether-key", key) returns
  // '[data-tether-key="<escaped>"]'
  function attrSelector(attr, value) {
    return "[" + attr + '="' + CSS.escape(value) + '"]';
  }

  // --- Initialisation ---

  document.addEventListener("DOMContentLoaded", function () {
    root = document.querySelector("[data-tether-root]");
    if (!root) return;

    endpoint = root.getAttribute("data-tether-endpoint") || "";
    sessionID = root.getAttribute("data-tether-session") || "";
    transportMode = root.getAttribute("data-tether-transport") || "ws";
    initialRetryDelay = parseInt(root.getAttribute("data-tether-retry-delay")) || 500;
    retryDelay = initialRetryDelay;
    maxRetryDelay = parseInt(root.getAttribute("data-tether-max-retry-delay")) || 10000;
    backoffMultiplier = parseFloat(root.getAttribute("data-tether-backoff-multiplier")) || 1.5;
    jitter = root.hasAttribute("data-tether-jitter");
    defaultDebounce = parseInt(root.getAttribute("data-tether-debounce-default")) || 300;
    transitionTimeout = parseInt(root.getAttribute("data-tether-transition-timeout")) || 5000;
    devMode = root.hasAttribute("data-tether-dev");
    backgroundSync = root.hasAttribute("data-tether-background-sync");
    syncRetention = parseInt(root.getAttribute("data-tether-sync-retention")) || 3600000;
    flashDuration = parseInt(root.getAttribute("data-tether-flash-duration")) || 5000;
    toastDuration = parseInt(root.getAttribute("data-tether-toast-duration")) || 5000;
    // Remove cloak attributes so hidden elements become visible now
    // that the runtime is ready. The server injects a style rule that
    // hides [data-tether-cloak] elements before JS loads.
    var cloaked = document.querySelectorAll("[data-tether-cloak]");
    for (var i = 0; i < cloaked.length; i++) cloaked[i].removeAttribute("data-tether-cloak");

    initViewportObserver();
    scanTimers();

    if (transportMode === "fetch") {
      connectionMode = "fetch";
      if (root) root.setAttribute("data-tether-state", "connected");
      bindEvents();
      mountExistingHooks();
      applyValidation(root);
      bindEditables(root);
      observeViewportElements(root);
    } else {
      connectionMode = (transportMode === "sse") ? "sse" : "ws";
      if (root) root.setAttribute("data-tether-state", "connecting");
      connect();
      bindEvents();
      applyValidation(root);
      bindEditables(root);
      observeViewportElements(root);
    }

    // On page unload, send a beacon so the server can destroy the
    // session immediately instead of waiting for the disconnect timer.
    // sendBeacon is fire-and-forget but works for the common case of
    // clean navigations and tab closes. The sessionStorage handoff
    // covers the cases where beforeunload doesn't fire (crash, kill).
    window.addEventListener("beforeunload", function () {
      // Close the WebSocket with a normal close code (1000) so
      // Firefox doesn't apply its RFC 6455 reconnection throttle.
      // Without this, Firefox sees the page-unload connection drop
      // as an abnormal termination and delays the next WebSocket
      // connection by up to 60 seconds with exponential backoff.
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.close(1000, "page unload");
      }
      if (eventSource) {
        eventSource.close();
      }
      if (sessionID) {
        navigator.sendBeacon(endpoint + "?destroy=" + sessionID);
      }
    });

    // Dev mode: expose a disconnect helper for integration testing.
    // Closes the transport so the server sees a clean disconnect.
    // Not available in production - devMode is only set when the
    // server includes data-tether-dev on the root element.
    if (devMode) {
      window.Tether.disconnect = function () {
        if (ws) ws.close();
        if (eventSource) eventSource.close();
      };
    }

    // Dev mode: unregister the service worker scoped to this handler so
    // cached assets are never served stale during development. Only the
    // worker matching this endpoint's scope is removed - workers
    // registered by other handlers on the same origin are left alone.
    if (devMode && "serviceWorker" in navigator) {
      var devScope = new URL(endpoint || "/", location.href).href;
      navigator.serviceWorker.getRegistrations().then(function (regs) {
        for (var i = 0; i < regs.length; i++) {
          if (regs[i].scope === devScope) regs[i].unregister();
        }
      });
    } else if (root.hasAttribute("data-tether-worker") && "serviceWorker" in navigator) {
      // Full service worker: asset caching, offline page shells, push
      // notification handling, and background sync for SSE resilience.
      // Pass sync retention so the worker discards stale events
      // consistently with the main thread.
      var workerURL = "/_tether/tether-worker.js?syncRetention=" + syncRetention;
      navigator.serviceWorker.register(workerURL, { scope: endpoint || "/" })
        .catch(function (err) {
          reportError("worker", "service worker registration failed: " + err);
        });
    } else if (root.hasAttribute("data-tether-push-key") && "serviceWorker" in navigator) {
      // Push-only service worker: receives push events and shows
      // notifications without intercepting fetch requests or caching.
      navigator.serviceWorker.register("/_tether/tether-push-worker.js", { scope: endpoint || "/" })
        .catch(function (err) {
          reportError("worker", "push worker registration failed: " + err);
        });
    }

    // Subscribe to push when the user clicks a [data-tether-push-subscribe]
    // element. This ensures the browser permission prompt fires from a
    // genuine user gesture.
    root.addEventListener("click", function (e) {
      var el = e.target.closest("[data-tether-push-subscribe]");
      if (!el) return;
      var pushKey = root.getAttribute("data-tether-push-key");
      if (!pushKey || !("PushManager" in window) || !("serviceWorker" in navigator)) return;
      navigator.serviceWorker.ready.then(function (reg) {
        subscribePush(reg, pushKey);
      });
    });

    // Signal actions (toggle/set) are bound on document - not root  - 
    // because signal bindings query the whole document. This lets signal
    // actions on elements in the Layout shell (outside the morphed root)
    // work the same as those inside it.
    document.addEventListener("click", handleSignalActions);
  });

  // --- Connection ---
  //
  // Connects according to transportMode: "ws" uses WebSocket only,
  // "sse" uses SSE+POST only, "auto" tries WebSocket first and falls
  // back to SSE+POST if the initial WebSocket connection fails.

  function connect() {
    if (connectionMode === "sse") {
      connectSSE();
    } else {
      connectWS();
    }
  }

  function storageKey() { return "tether_session_" + endpoint; }

  function connectWS() {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var url = protocol + "//" + location.host + endpoint;
    if (sessionID) url += "?session=" + sessionID;

    // If a previous session exists in sessionStorage (from a page
    // refresh), tell the server to destroy it immediately rather
    // than waiting for the 30s disconnect timer.
    var prev = sessionStorage.getItem(storageKey());
    if (prev && prev !== sessionID) {
      url += (url.indexOf("?") === -1 ? "?" : "&") + "replaces=" + prev;
    }

    ws = new WebSocket(url);

    ws.onopen = function () {
      // On reconnect, sync the current URL with the server in case
      // the user navigated via back/forward while disconnected. The
      // browser's popstate fires even when offline, changing the URL
      // without notifying the server.
      var isReconnect = wsOpened;
      wsOpened = true;
      retryDelay = initialRetryDelay;
      if (root) root.setAttribute("data-tether-state", "connected");
      hideReconnectBar();
      resyncPushSubscription();
      // Store the session ID so a page refresh can tell the server
      // to destroy this session immediately via the replaces param.
      if (sessionID) sessionStorage.setItem(storageKey(), sessionID);
      if (isReconnect) {
        // Sync the current URL with the server - the user may have
        // navigated via back/forward while disconnected.
        sendNavigate(location.pathname + location.search);
      } else {
        mountExistingHooks();
      }
    };

    ws.onmessage = function (e) {
      var msg;
      try {
        msg = window.Tether.decode(e.data);
      } catch (err) {
        reportError("parse", "failed to parse WebSocket message: " + err, true);
        return;
      }
      applyMessage(msg);
    };

    ws.onclose = function () {
      if (root) root.setAttribute("data-tether-state", "disconnected");
      showReconnectBar();
      // If the WebSocket never connected and the server allows SSE
      // fallback (transportMode "auto"), switch to SSE+POST permanently.
      if (!wsOpened && transportMode === "auto") {
        connectionMode = "sse";
        connectSSE();
      } else {
        scheduleReconnect();
      }
    };

    ws.onerror = function () {
      ws.close();
    };
  }

  function connectSSE() {
    var url = location.protocol + "//" + location.host + endpoint;
    if (sessionID) url += "?session=" + sessionID;

    var prev = sessionStorage.getItem(storageKey());
    if (prev && prev !== sessionID) {
      url += (url.indexOf("?") === -1 ? "?" : "&") + "replaces=" + prev;
    }

    eventSource = new EventSource(url);

    eventSource.onopen = function () {
      // On reconnect, sync the current URL with the server in case
      // the user navigated via back/forward while disconnected.
      var isReconnect = sseOpened;
      sseOpened = true;
      retryDelay = initialRetryDelay;
      if (root) root.setAttribute("data-tether-state", "connected");
      hideReconnectBar();
      resyncPushSubscription();
      if (sessionID) sessionStorage.setItem(storageKey(), sessionID);
      if (isReconnect) {
        if (backgroundSync) replayQueuedEvents();
        sendNavigate(location.pathname + location.search);
      } else {
        mountExistingHooks();
      }
    };

    eventSource.onmessage = function (e) {
      var msg;
      try {
        msg = window.Tether.decode(e.data);
      } catch (err) {
        reportError("parse", "failed to parse SSE message: " + err, true);
        return;
      }
      applyMessage(msg);
    };

    eventSource.onerror = function () {
      if (root) root.setAttribute("data-tether-state", "disconnected");
      showReconnectBar();
      // EventSource reconnects automatically - no manual retry needed.
    };
  }

  function scheduleReconnect() {
    var delay = retryDelay;
    // Jitter spreads reconnection attempts across time to prevent
    // synchronised waves (thundering herd) after a server restart.
    // The delay is multiplied by a random factor in [0.5, 1.0).
    if (jitter) delay = Math.floor(delay * (0.5 + Math.random() * 0.5));
    setTimeout(function () {
      if (root) root.setAttribute("data-tether-state", "connecting");
      retryDelay = Math.min(retryDelay * backoffMultiplier, maxRetryDelay);
      connect();
    }, delay);
  }

  // --- Reconnecting indicator ---
  //
  // A fixed bar at the top of the viewport that slides in when the
  // transport disconnects and slides out on reconnect. Created lazily
  // on first disconnect so there is no DOM cost when the connection
  // stays healthy. Developers can override the appearance via the
  // .tether-reconnecting CSS class.

  var reconnectBar = null;

  function createReconnectBar() {
    var bar = document.createElement("div");
    bar.className = "tether-reconnecting";
    bar.setAttribute("role", "status");
    bar.setAttribute("aria-live", "polite");
    bar.textContent = "Reconnecting\u2026";
    // Structural styles are inline so the bar works without any CSS.
    // Cosmetic styles (background, colour, font, padding) live on the
    // .tether-reconnecting class so developers can override them.
    bar.style.cssText = [
      "position:fixed",
      "top:0",
      "left:0",
      "right:0",
      "z-index:2147483647",
      "transform:translateY(-100%)",
      "transition:transform .3s ease",
      "pointer-events:none"
    ].join(";");
    document.body.appendChild(bar);
    return bar;
  }

  function showReconnectBar() {
    if (!reconnectBar) reconnectBar = createReconnectBar();
    reconnectBar.textContent = "Reconnecting\u2026";
    // Force reflow before changing the transform so the browser
    // registers the initial off-screen position.
    reconnectBar.offsetHeight;
    reconnectBar.style.transform = "translateY(0)";
  }

  function hideReconnectBar() {
    if (reconnectBar) {
      reconnectBar.style.transform = "translateY(-100%)";
    }
  }

  // --- Push notification subscription ---
  //
  // Push subscription is deferred until the user clicks an element with
  // data-tether-push-subscribe. Browsers require a user gesture for
  // pushManager.subscribe - auto-prompting causes permission denials
  // and can permanently block the site from ever prompting again.
  //
  // On every connect (including reconnects), resyncPushSubscription
  // checks whether the browser already holds a valid subscription for
  // the current VAPID key and re-sends it to the server so the new
  // session can use it immediately. Stale subscriptions bound to old
  // VAPID keys are unsubscribed silently - the user will need to
  // click the subscribe button again to create a fresh one.

  // Compare the existing subscription's applicationServerKey against
  // the VAPID key the server advertises. Returns true when they match.
  function pushKeyMatches(sub, vapidKey) {
    var subKey = sub.options && sub.options.applicationServerKey;
    if (!subKey) return false;
    var expected = urlBase64ToUint8Array(vapidKey);
    var actual = new Uint8Array(subKey);
    if (expected.length !== actual.length) return false;
    for (var i = 0; i < expected.length; i++) {
      if (expected[i] !== actual[i]) return false;
    }
    return true;
  }

  // resyncPushSubscription runs on every transport connect. If the
  // browser holds a subscription matching the current VAPID key, it
  // is sent to the server so the session's pushSub is populated. If
  // the subscription was created with a different VAPID key (e.g. the
  // server restarted and generated new keys), it is unsubscribed so
  // a clean subscribe can happen later via the button.
  function resyncPushSubscription() {
    var pushKey = root.getAttribute("data-tether-push-key");
    if (!pushKey || !("serviceWorker" in navigator) || !("PushManager" in window)) return;
    navigator.serviceWorker.ready.then(function (reg) {
      reg.pushManager.getSubscription().then(function (sub) {
        if (!sub) return;
        if (pushKeyMatches(sub, pushKey)) {
          sendPushSubscription(sub);
        } else {
          // VAPID key changed - subscription is useless, discard it.
          sub.unsubscribe().catch(function (err) {
            reportError("push", "unsubscribe failed: " + err);
          });
        }
      });
    });
  }

  function subscribePush(reg, vapidKey) {
    reg.pushManager.getSubscription().then(function (sub) {
      if (sub) {
        if (pushKeyMatches(sub, vapidKey)) {
          // Already subscribed with the current key - re-send in case
          // the server restarted and lost the session's subscription.
          sendPushSubscription(sub);
          return;
        }
        // Subscription bound to old VAPID keys - discard and
        // resubscribe so the push service accepts messages signed
        // with the current key.
        return sub.unsubscribe().then(function () {
          return reg.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(vapidKey)
          });
        }).then(function (newSub) {
          sendPushSubscription(newSub);
        });
      }
      return reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapidKey)
      }).then(function (sub) {
        sendPushSubscription(sub);
      });
    }).catch(function (err) {
      reportError("push", "push subscription failed: " + err);
    });
  }

  function sendPushSubscription(sub) {
    var url = location.protocol + "//" + location.host + endpoint;
    var opts = {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Tether-Session": sessionID,
        "Tether-Push-Subscribe": "true"
      },
      body: JSON.stringify(sub.toJSON())
    };
    fetch(url, opts).catch(function () {
      // Retry once after a short delay - covers transient network
      // blips during mobile handoffs or server rolling deploys.
      setTimeout(function () {
        fetch(url, opts).catch(function (err) {
          reportError("push", "push subscription POST failed: " + err);
        });
      }, 2000);
    });
  }

  // Convert a base64url-encoded string to a Uint8Array for the
  // PushManager.subscribe applicationServerKey parameter.
  function urlBase64ToUint8Array(base64String) {
    var padding = "=".repeat((4 - base64String.length % 4) % 4);
    var base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
    var raw = atob(base64);
    var arr = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i);
    return arr;
  }

  // --- SSE event resilience ---
  //
  // When an SSE POST event fails (network down, server restarting),
  // the event payload is queued in IndexedDB for replay on reconnect.
  // If Background Sync is available, a sync is also registered so the
  // service worker can replay events even if the tab was closed.

  var EVENT_DB_NAME = "tether-events";
  var EVENT_DB_VERSION = 1;
  var EVENT_STORE = "queue";

  function openEventDB() {
    return new Promise(function (resolve, reject) {
      if (!("indexedDB" in window)) { reject(new Error("no IndexedDB")); return; }
      var req = indexedDB.open(EVENT_DB_NAME, EVENT_DB_VERSION);
      req.onupgradeneeded = function (e) {
        e.target.result.createObjectStore(EVENT_STORE, { autoIncrement: true });
      };
      req.onsuccess = function (e) { resolve(e.target.result); };
      req.onerror = function (e) { reject(e.target.error); };
    });
  }

  function queueFailedEvent(payload) {
    openEventDB().then(function (db) {
      var tx = db.transaction(EVENT_STORE, "readwrite");
      tx.objectStore(EVENT_STORE).add({
        sessionID: sessionID,
        endpoint: location.protocol + "//" + location.host + endpoint,
        payload: payload,
        ts: Date.now()
      });
    }).then(function () {
      // Register background sync so the service worker can replay
      // queued events even if the tab is closed before reconnect.
      if ("serviceWorker" in navigator && "SyncManager" in window) {
        navigator.serviceWorker.ready.then(function (reg) {
          reg.sync.register("tether-event-sync");
        });
      }
    }).catch(function (err) {
      reportError("indexeddb", "failed to queue event: " + err, true);
    });
  }

  function replayQueuedEvents() {
    // When a service worker is active, delegate replay to it via
    // Background Sync. The worker replays events for all sessions and
    // is already listening for the sync event. Replaying from both the
    // main thread and the worker would cause duplicate POSTs.
    if (navigator.serviceWorker && navigator.serviceWorker.controller && "SyncManager" in window) {
      navigator.serviceWorker.ready.then(function (reg) {
        reg.sync.register("tether-event-sync");
      });
      return;
    }

    // No active worker - replay from the main thread as a fallback.
    openEventDB().then(function (db) {
      var tx = db.transaction(EVENT_STORE, "readonly");
      var store = tx.objectStore(EVENT_STORE);
      var allReq = store.getAll();
      var keysReq = store.getAllKeys();
      tx.oncomplete = function () {
        var events = allReq.result;
        var keys = keysReq.result;
        var url = location.protocol + "//" + location.host + endpoint;
        var now = Date.now();
        for (var i = 0; i < events.length; i++) {
          // Delete orphaned events from previous sessions.
          if (events[i].sessionID !== sessionID) {
            deleteEventFromDB(db, keys[i]);
            continue;
          }
          // Discard events older than the retention window.
          if (now - events[i].ts > syncRetention) {
            deleteEventFromDB(db, keys[i]);
            continue;
          }
          replayAndDeleteEvent(db, keys[i], events[i].payload, url);
        }
      };
    }).catch(function (err) {
      reportError("indexeddb", "failed to replay queued events: " + err, true);
    });
  }

  function deleteEventFromDB(db, key) {
    var tx = db.transaction(EVENT_STORE, "readwrite");
    tx.objectStore(EVENT_STORE).delete(key);
  }

  function replayAndDeleteEvent(db, key, payload, url) {
    fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Tether-Session": sessionID
      },
      body: payload
    }).then(function (resp) {
      // Delete on success or permanent client error (4xx). Keep on
      // server error (5xx) so the next sync attempt can retry.
      if (resp.ok || (resp.status >= 400 && resp.status < 500)) {
        deleteEventFromDB(db, key);
      }
    }).catch(function (err) {
      reportError("fetch", "event replay failed: " + err, true);
    });
  }

  // --- Message handling ---

  function applyMessage(msg) {
    if (msg.type !== "update") return;

    // Batch all DOM mutations into a single animation frame so the
    // browser coalesces reflows and repaints. Without this, each
    // patch and morph triggers a separate layout pass. restorePending
    // runs inside the frame so it is synchronised with the DOM changes
    // it correlates with.
    requestAnimationFrame(function () {
      restorePending(msg.event_id);

      // Server reassigned session ID (stale client reconnection).
      if (msg.session) {
        sessionID = msg.session;
        if (root) {
          root.setAttribute("data-tether-session", sessionID);
        }
      }

      // Apply content patches first, then structural morphs.
      if (msg.patches) {
        for (var i = 0; i < msg.patches.length; i++) {
          applyPatch(msg.patches[i]);
        }
      }
      if (msg.morphs) {
        for (var i = 0; i < msg.morphs.length; i++) {
          applyMorph(msg.morphs[i]);
        }
      }

      // Rebuild the hotkey registry after DOM changes so newly added
      // or removed hotkey elements are reflected.
      if (msg.patches || msg.morphs) {
        buildHotkeyRegistry();
      }

      if (msg.url) {
        if (msg.replace) {
          history.replaceState({}, "", msg.url);
        } else {
          history.pushState({}, "", msg.url);
          // Server-driven navigation: pushState changes the URL but
          // does not trigger popstate, so the server never learns about
          // the new URL. Send a navigate event so OnNavigate can update
          // state and re-render for the target page.
          sendNavigate(msg.url);
        }
      }
      if (msg.title) {
        document.title = msg.title;
      }
      if (msg.flash) {
        for (var selector in msg.flash) {
          var el = safeQuery(document, selector);
          if (el) {
            el.textContent = msg.flash[selector];
            (function (target) {
              setTimeout(function () { target.textContent = ""; }, flashDuration);
            })(el);
          }
        }
      }
      if (msg.announce) {
        announce(msg.announce);
      }
      if (msg.toast) {
        toast(msg.toast);
      }
      if (msg.scroll_to) {
        var scrollTarget = safeQuery(document, msg.scroll_to);
        if (scrollTarget) scrollTarget.scrollIntoView({ behavior: "smooth", block: "nearest" });
      }
      if (msg.download) {
        var a = document.createElement("a");
        a.href = msg.download;
        a.download = "";
        a.style.display = "none";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
      }
      if (msg.signals) {
        applySignals(msg.signals);
      }

      // Set focus on the designated element after all DOM updates.
      // Uses data-tether-autofocus (not data-tether-focus, which is the
      // event binding attribute for the Focus helper).
      var focusEl = root.querySelector("[data-tether-autofocus]");
      if (focusEl) focusEl.focus();

      // Lazy-load extension scripts when their marker attributes first
      // appear in the DOM after a morph. This eliminates the need for
      // hidden marker elements on the initial page.
      loadExtensions();
      scanTimers();
      applyValidation(root);
      bindEditables(root);

      // Notify extensions that the DOM has been updated so they can
      // re-scan for new elements (e.g. upload triggers added by a morph).
      document.dispatchEvent(new CustomEvent("tether:update", { detail: { root: root } }));
    });
  }

  // --- Accessibility live region ---
  //
  // A visually hidden aria-live region for screen reader announcements.
  // Created lazily on first use. The server sends announce text via
  // Session.Announce and the JS populates this element, causing
  // assistive technology to read it aloud.

  var liveRegion = null;

  function ensureLiveRegion() {
    if (liveRegion) return liveRegion;
    liveRegion = document.createElement("div");
    liveRegion.setAttribute("role", "status");
    liveRegion.setAttribute("aria-live", "polite");
    liveRegion.setAttribute("aria-atomic", "true");
    liveRegion.style.cssText = [
      "position:absolute",
      "width:1px",
      "height:1px",
      "padding:0",
      "margin:-1px",
      "overflow:hidden",
      "clip:rect(0,0,0,0)",
      "white-space:nowrap",
      "border:0"
    ].join(";");
    document.body.appendChild(liveRegion);
    return liveRegion;
  }

  function announce(text) {
    var region = ensureLiveRegion();
    // Clear first so repeated identical announcements still trigger.
    region.textContent = "";
    requestAnimationFrame(function () {
      region.textContent = text;
    });
  }

  // --- Global toasts ---
  //
  // Transient notifications that float over the UI. The client JS
  // manages their lifecycle (insertion, animation, removal) so the
  // server can push feedback without coordinating with the page layout.

  var toastContainer = null;

  function ensureToastContainer() {
    if (toastContainer) return toastContainer;
    toastContainer = document.createElement("div");
    toastContainer.className = "tether-toast-container";
    toastContainer.style.cssText = [
      "position:fixed",
      "bottom:24px",
      "left:50%",
      "transform:translateX(-50%)",
      "z-index:2147483647",
      "display:flex",
      "flex-direction:column-reverse",
      "gap:8px",
      "pointer-events:none"
    ].join(";");
    document.body.appendChild(toastContainer);
    return toastContainer;
  }

  function toast(text) {
    var container = ensureToastContainer();
    var el = document.createElement("div");
    el.className = "tether-toast";
    el.textContent = text;
    // Structural styles are inline; cosmetic styles (background,
    // colour, font, border-radius, shadow) live on .tether-toast.
    el.style.cssText = [
      "opacity:0",
      "transform:translateY(20px)",
      "transition:opacity .3s, transform .3s",
      "pointer-events:auto"
    ].join(";");

    container.appendChild(el);

    // Animate in
    requestAnimationFrame(function () {
      el.offsetHeight;
      el.style.opacity = "1";
      el.style.transform = "translateY(0)";
    });

    // Animate out and remove. The fallback timeout ensures the element
    // is removed even when CSS transitions are disabled (e.g.
    // prefers-reduced-motion) and transitionend never fires.
    setTimeout(function () {
      el.style.opacity = "0";
      el.style.transform = "translateY(20px)";
      var removed = false;
      function remove() {
        if (removed) return;
        removed = true;
        if (el.parentNode) el.parentNode.removeChild(el);
      }
      el.addEventListener("transitionend", remove);
      setTimeout(remove, 1000);
    }, toastDuration);
  }

  // --- JS hooks ---
  //
  // Elements with data-tether-hook="name" receive lifecycle callbacks
  // when they are added, morphed, or removed from the DOM. Hooks are
  // registered on the global Tether.hooks object.

  function callHook(el, lifecycle) {
    var name = el.getAttribute("data-tether-hook");
    if (!name) return;
    var hook = window.Tether.hooks[name];
    if (hook && typeof hook[lifecycle] === "function") {
      hook[lifecycle](el);
    }
  }

  // callHookDeep invokes a lifecycle callback on the element itself and
  // on any descendant hook elements. Idiomorph only fires afterNodeAdded
  // for the top-level node - descendants are already part of its innerHTML
  // and need to be scanned separately.
  function callHookDeep(el, lifecycle) {
    callHook(el, lifecycle);
    var hookEls = el.querySelectorAll("[data-tether-hook]");
    for (var i = 0; i < hookEls.length; i++) {
      callHook(hookEls[i], lifecycle);
    }
  }

  // mountExistingHooks scans the DOM for hook elements that were rendered
  // in the initial HTML (before any morph). Called once on first connect
  // so hooks fire even when the page loads directly onto a hooked view.
  function mountExistingHooks() {
    if (!root) return;
    var hookEls = root.querySelectorAll("[data-tether-hook]");
    for (var i = 0; i < hookEls.length; i++) {
      callHook(hookEls[i], "mounted");
    }
  }

  // --- Loading / pending states ---
  //
  // Elements with data-tether-disable are disabled while an event is in
  // flight. The attribute value, if non-empty, replaces the element's
  // text content during the wait. All pending elements are restored
  // when the next server update arrives.

  function disablePending(el, eventID) {
    var entry = { el: el, disabled: el.hasAttribute("disabled") };

    if (el.hasAttribute("data-tether-disable")) {
      entry.text = el.textContent;
      var newText = el.getAttribute("data-tether-disable");
      el.setAttribute("disabled", "");
      if (newText) el.textContent = newText;
    }

    var indicatorSelector = el.getAttribute("data-tether-indicator");
    if (indicatorSelector) {
      entry.indicator = safeQuery(document, indicatorSelector);
      if (entry.indicator) entry.indicator.classList.add("tether-pending");
    }

    if (++pendingCount === 1 && root) {
      root.classList.add("tether-loading");
    }

    pendingElements[eventID] = entry;
  }

  function restorePending(eventID) {
    if (!eventID) return;
    var entry = pendingElements[eventID];
    if (!entry) return;
    if (!entry.disabled) entry.el.removeAttribute("disabled");
    if (entry.text !== undefined) entry.el.textContent = entry.text;
    if (entry.indicator) entry.indicator.classList.remove("tether-pending");

    if (--pendingCount === 0 && root) {
      root.classList.remove("tether-loading");
    }

    delete pendingElements[eventID];
  }

  // --- Client state preservation ---
  //
  // Client-side toggles (data-tether-toggle-class, data-tether-toggle-attr)
  // modify the DOM without the server knowing. When a server morph arrives,
  // the new HTML won't contain the toggled classes or attributes. The
  // beforeNodeMorphed hook copies client-managed state onto the incoming
  // node so Idiomorph merges it into the live DOM.

  function preserveClientState(oldNode, newNode) {
    var trackedClasses = oldNode.getAttribute("data-tether-client-classes");
    if (trackedClasses) {
      var names = trackedClasses.split(/\s+/);
      for (var i = 0; i < names.length; i++) {
        if (!names[i]) continue;
        if (oldNode.classList.contains(names[i])) {
          newNode.classList.add(names[i]);
        } else {
          newNode.classList.remove(names[i]);
        }
      }
      newNode.setAttribute("data-tether-client-classes", trackedClasses);
    }

    var trackedAttrs = oldNode.getAttribute("data-tether-client-attrs");
    if (trackedAttrs) {
      var names = trackedAttrs.split(/\s+/);
      for (var i = 0; i < names.length; i++) {
        if (!names[i]) continue;
        if (oldNode.hasAttribute(names[i])) {
          newNode.setAttribute(names[i], "");
        } else {
          newNode.removeAttribute(names[i]);
        }
      }
      newNode.setAttribute("data-tether-client-attrs", trackedAttrs);
    }
  }

  var morphCallbacks = {
    beforeNodeAdded: function (newNode) {
      if (newNode.nodeType !== 1) return true;
      var name = newNode.getAttribute("data-tether-transition");
      if (name) {
        newNode.classList.add("tether-" + name + "-enter");
      }
      return true;
    },

    afterNodeAdded: function (newNode) {
      if (newNode.nodeType !== 1) return;
      callHookDeep(newNode, "mounted");
      reapplySignals(newNode);
      observeViewportElements(newNode);
      var name = newNode.getAttribute("data-tether-transition");
      if (!name) return;
      // Force reflow so the browser registers the enter class,
      // then remove it to trigger the CSS transition.
      newNode.offsetHeight;
      newNode.classList.remove("tether-" + name + "-enter");
    },

    beforeNodeMorphed: function (oldNode, newNode) {
      if (oldNode.nodeType !== 1) return true;

      // Permanent elements are never morphed - their subtree is left
      // untouched. Used for video players, iframes, and third-party
      // widgets that manage their own DOM.
      if (oldNode.hasAttribute("data-tether-permanent")) return false;

      // Cancel any pending leave transition - the element is being
      // morphed back in rather than removed.
      if (leavingNodes.has(oldNode)) {
        leavingNodes.delete(oldNode);
        var name = oldNode.getAttribute("data-tether-transition");
        if (name) {
          oldNode.classList.remove("tether-" + name + "-leave");
        }
      }

      // Save scroll position for containers marked with
      // data-tether-preserve-scroll so it survives the morph.
      if (oldNode.hasAttribute("data-tether-preserve-scroll")) {
        oldNode._tetherScrollTop = oldNode.scrollTop;
      }

      preserveClientState(oldNode, newNode);
      return true;
    },

    afterNodeMorphed: function (oldNode) {
      if (oldNode.nodeType !== 1) return;

      // Restore saved scroll position after morph.
      if (oldNode._tetherScrollTop !== undefined) {
        oldNode.scrollTop = oldNode._tetherScrollTop;
        delete oldNode._tetherScrollTop;
      }

      // Auto-scroll containers to the bottom after content updates.
      // Checks the morphed node and any auto-scroll descendants.
      if (oldNode.hasAttribute("data-tether-auto-scroll")) {
        oldNode.scrollTop = oldNode.scrollHeight;
      }
      oldNode.querySelectorAll("[data-tether-auto-scroll]").forEach(function (el) {
        el.scrollTop = el.scrollHeight;
      });

      callHook(oldNode, "updated");
      reapplySignals(oldNode);
      observeViewportElements(oldNode);
    },

    beforeNodeRemoved: function (oldNode) {
      if (oldNode.nodeType !== 1) return true;
      callHookDeep(oldNode, "destroyed");
      var name = oldNode.getAttribute("data-tether-transition");
      if (!name) return true;

      // Already leaving - let it finish
      if (leavingNodes.has(oldNode)) return false;

      leavingNodes.add(oldNode);
      oldNode.classList.add("tether-" + name + "-leave");

      function remove() {
        leavingNodes.delete(oldNode);
        if (oldNode.parentNode) {
          oldNode.parentNode.removeChild(oldNode);
        }
      }

      var fallbackTimer = setTimeout(remove, transitionTimeout);

      oldNode.addEventListener("transitionend", function handler() {
        oldNode.removeEventListener("transitionend", handler);
        clearTimeout(fallbackTimer);
        remove();
      });

      return false; // prevent immediate removal
    },

    afterNodeRemoved: function (oldNode) {
      if (oldNode.nodeType === 1) {
        leavingNodes.delete(oldNode);
      }
    }
  };

  // --- Viewport trigger ---
  //
  // Elements with data-tether-viewport fire a server event when they
  // enter the viewport. Uses a single IntersectionObserver instance.
  // Each element fires once and is then unobserved; after a morph
  // replaces it, the new element is observed again via afterNodeAdded.

  var viewportObserver = null;

  function initViewportObserver() {
    if (!("IntersectionObserver" in window)) return;
    viewportObserver = new IntersectionObserver(function (entries) {
      for (var i = 0; i < entries.length; i++) {
        if (!entries[i].isIntersecting) continue;
        var el = entries[i].target;
        var action = el.getAttribute("data-tether-viewport");
        if (action) {
          var data = {};
          for (var j = 0; j < el.attributes.length; j++) {
            var attr = el.attributes[j];
            if (attr.name.indexOf(eventDataPrefix) === 0) {
              data[attr.name.substring(eventDataPrefix.length)] = attr.value;
            }
          }
          sendEvent("viewport", action, data);
        }
        viewportObserver.unobserve(el);
      }
    }, {
      threshold: 0
    });
  }

  function observeViewportElements(container) {
    if (!viewportObserver) return;
    var els = container.querySelectorAll
      ? container.querySelectorAll("[data-tether-viewport]")
      : [];
    for (var i = 0; i < els.length; i++) {
      viewportObserver.observe(els[i]);
    }
    // The container itself might be a viewport element (e.g. a sentinel
    // div inserted by a morph).
    if (container.hasAttribute && container.hasAttribute("data-tether-viewport")) {
      viewportObserver.observe(container);
    }
  }

  // --- Patching and morphing ---

  function applyPatch(patch) {
    var el = safeQuery(document, attrSelector("data-tether-key", patch.key));
    if (!el) return;

    if (devMode) {
      console.log("tether: patch", patch.key);
    }

    var template = document.createElement("template");
    template.innerHTML = patch.html;
    if (template.content.childElementCount > 1) {
      reportError("render", "patch for key '" + patch.key + "' contains multiple root elements; only the first will be used");
    }
    var newEl = template.content.firstElementChild;
    if (!newEl) return;

    Idiomorph.morph(el, newEl, {callbacks: morphCallbacks});
  }

  function applyMorph(morph) {
    if (devMode) {
      console.log("tether: morph", morph.key || "root");
    }

    if (!morph.key) {
      // Empty key targets the root element. Pass the HTML string
      // directly so idiomorph parses it into a DocumentFragment
      // whose children are the rendered tree. With innerHTML mode
      // this morphs root's children to match the fragment's
      // children, preserving the outermost wrapper element (e.g.
      // div.shell) across renders. Pre-parsing and passing the
      // firstElementChild would strip that wrapper because
      // idiomorph would use the element's children instead.
      if (root && morph.html) {
        Idiomorph.morph(root, morph.html, {morphStyle: "innerHTML", callbacks: morphCallbacks});
      }
    } else {
      // Scoped morph targets a keyed container.
      var template = document.createElement("template");
      template.innerHTML = morph.html;
      if (template.content.childElementCount > 1) {
        reportError("render", "morph for key '" + morph.key + "' contains multiple root elements; only the first will be used");
      }
      var newEl = template.content.firstElementChild;
      if (!newEl) return;
      var el = safeQuery(document, attrSelector("data-tether-key", morph.key));
      if (el) {
        Idiomorph.morph(el, newEl, {callbacks: morphCallbacks});
      }
    }
  }

  // --- Signals ---
  //
  // Signals are reactive key/value pairs pushed by the server. Elements
  // bind to signals via data-tether-bind-* attributes. When a signal
  // changes, all bound elements update instantly - no render, no diff.
  // Signal values are stored in Tether.signals so JS hooks can read them.

  function applySignals(updates) {
    for (var key in updates) {
      window.Tether.signals[key] = updates[key];
      updateSignalBindings(key, updates[key]);
      handleTimerSignal(key, updates[key]);
    }
  }

  // updateSignalBindings applies a signal value to all bound elements
  // in the document - not just inside the tether root. This allows signal
  // bindings on elements in the Layout shell (nav highlights, body
  // classes, status indicators) that sit outside the morphed content area.
  function updateSignalBindings(key, value) {
    // Text bindings: data-tether-bind-text="signalName"
    var els = safeQueryAll(document, attrSelector("data-tether-bind-text", key));
    for (var i = 0; i < els.length; i++) {
      els[i].textContent = value == null ? "" : String(value);
    }

    // Show bindings: data-tether-bind-show="signalName"
    els = safeQueryAll(document, attrSelector("data-tether-bind-show", key));
    for (var i = 0; i < els.length; i++) {
      els[i].style.display = isTruthy(value) ? "" : "none";
    }

    // Hide bindings: data-tether-bind-hide="signalName"
    els = safeQueryAll(document, attrSelector("data-tether-bind-hide", key));
    for (var i = 0; i < els.length; i++) {
      els[i].style.display = isTruthy(value) ? "none" : "";
    }

    // Class bindings: data-tether-bind-class="className signalName"
    els = document.querySelectorAll("[data-tether-bind-class]");
    for (var i = 0; i < els.length; i++) {
      var parts = els[i].getAttribute("data-tether-bind-class").split(/\s+/);
      if (parts.length === 2 && parts[1] === key) {
        els[i].classList.toggle(parts[0], isTruthy(value));
      }
    }

    // Attr bindings: data-tether-bind-attr="attrName signalName"
    els = document.querySelectorAll("[data-tether-bind-attr]");
    for (var i = 0; i < els.length; i++) {
      var parts = els[i].getAttribute("data-tether-bind-attr").split(/\s+/);
      if (parts.length === 2 && parts[1] === key) {
        if (value === null || value === undefined || value === false) {
          els[i].removeAttribute(parts[0]);
        } else {
          els[i].setAttribute(parts[0], String(value));
        }
      }
    }

    // Value bindings: data-tether-bind-value="signalName"
    els = safeQueryAll(document, attrSelector("data-tether-bind-value", key));
    for (var i = 0; i < els.length; i++) {
      els[i].value = value == null ? "" : String(value);
    }
  }

  // parseSignalValue converts a string from an HTML data attribute into
  // a properly typed JS value. Data attributes are always strings, but
  // signals should hold typed values so that isTruthy works predictably:
  //   "true"  → true    "false" → false
  //   "42"    → 42      "3.14"  → 3.14
  //   "hello" → "hello" ""      → ""
  function parseSignalValue(str) {
    if (str === "true") return true;
    if (str === "false") return false;
    if (str !== "" && !isNaN(str)) return Number(str);
    return str;
  }

  // isTruthy evaluates signal truthiness for show/hide/class/attr
  // bindings. Signal values are always properly typed - booleans from
  // the server arrive as JSON booleans, strings from data attributes
  // are parsed by parseSignalValue before storage - so standard JS
  // falsy checks are sufficient.
  function isTruthy(val) {
    return val !== null && val !== undefined && val !== false && val !== 0 && val !== "";
  }

  // reapplySignals restores signal-bound values on an element and its
  // descendants after a morph replaces server-rendered content. Called
  // from the idiomorph afterNodeAdded and afterNodeMorphed callbacks.
  function reapplySignals(el) {
    applySignalsToElement(el);
    var bound = el.querySelectorAll(
      "[data-tether-bind-text],[data-tether-bind-show],[data-tether-bind-hide]," +
      "[data-tether-bind-class],[data-tether-bind-attr],[data-tether-bind-value]"
    );
    for (var i = 0; i < bound.length; i++) {
      applySignalsToElement(bound[i]);
    }
  }

  function applySignalsToElement(el) {
    if (el.nodeType !== 1) return;
    var signals = window.Tether.signals;

    var textSignal = el.getAttribute("data-tether-bind-text");
    if (textSignal && signals.hasOwnProperty(textSignal)) {
      el.textContent = signals[textSignal] == null ? "" : String(signals[textSignal]);
    }

    var showSignal = el.getAttribute("data-tether-bind-show");
    if (showSignal && signals.hasOwnProperty(showSignal)) {
      el.style.display = isTruthy(signals[showSignal]) ? "" : "none";
    }

    var hideSignal = el.getAttribute("data-tether-bind-hide");
    if (hideSignal && signals.hasOwnProperty(hideSignal)) {
      el.style.display = isTruthy(signals[hideSignal]) ? "none" : "";
    }

    var classBinding = el.getAttribute("data-tether-bind-class");
    if (classBinding) {
      var parts = classBinding.split(/\s+/);
      if (parts.length === 2 && signals.hasOwnProperty(parts[1])) {
        el.classList.toggle(parts[0], isTruthy(signals[parts[1]]));
      }
    }

    var attrBinding = el.getAttribute("data-tether-bind-attr");
    if (attrBinding) {
      var parts = attrBinding.split(/\s+/);
      if (parts.length === 2 && signals.hasOwnProperty(parts[1])) {
        var val = signals[parts[1]];
        if (val === null || val === undefined || val === false) {
          el.removeAttribute(parts[0]);
        } else {
          el.setAttribute(parts[0], String(val));
        }
      }
    }

    var valueSignal = el.getAttribute("data-tether-bind-value");
    if (valueSignal && signals.hasOwnProperty(valueSignal)) {
      el.value = signals[valueSignal] == null ? "" : String(signals[valueSignal]);
    }
  }

  // --- Event delegation ---

  var eventTypes = [
    ["click", "tether-click"],
    ["input", "tether-input"],
    ["change", "tether-change"],
    ["submit", "tether-submit"],
    ["keydown", "tether-keydown"],
    ["focus", "tether-focus"],
    ["blur", "tether-blur"],
    ["paste", "tether-paste"],
    ["contextmenu", "tether-contextmenu"]
  ];

  function bindEvents() {
    for (var i = 0; i < eventTypes.length; i++) {
      bindEventType(eventTypes[i][0], eventTypes[i][1]);
    }
    root.addEventListener("click", handleToggles);
    root.addEventListener("click", handleLinks);
    root.addEventListener("click", handleClipboard);
    root.addEventListener("click", handleScrollTo);
    root.addEventListener("click", handleSelectable);
    window.addEventListener("keydown", handleFocusTrap);
    window.addEventListener("keydown", handleHotkeys);
    buildHotkeyRegistry();

    window.addEventListener("popstate", function () {
      sendNavigate(location.pathname + location.search);
    });
  }

  // --- Client-side navigation ---
  //
  // Anchors with data-tether-link are intercepted. Instead of a full page
  // load the JS pushes the URL into the browser history and sends a
  // navigate event to the server so OnNavigate can update state.

  function handleLinks(e) {
    var link = e.target.closest("a[data-tether-link]");
    if (!link) return;

    // Let the browser handle modifier clicks (new tab, new window)
    // and links that explicitly target another frame.
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (link.getAttribute("target") === "_blank") return;

    var href = link.getAttribute("href");
    if (!href || href.indexOf("://") !== -1 || href.indexOf("//") === 0) return;

    e.preventDefault();
    // Only push to history if the event was actually sent to the
    // server. If the transport is down, changing the URL would create
    // a mismatch between the browser URL and the server's state.
    if (sendNavigate(href)) {
      history.pushState({}, "", href);
    }
  }

  function sendNavigate(url) {
    var idx = url.indexOf("?");
    var path = idx === -1 ? url : url.substring(0, idx);
    var search = idx === -1 ? "" : url.substring(idx + 1);
    return sendEvent("navigate", "", { path: path, search: search }) !== null;
  }

  // findPrefix walks up the DOM from el to the tether root, collecting
  // all data-tether-prefix values. Nested prefixes are joined with dots
  // (innermost last) so "group" > "left" produces "group.left". Returns
  // empty if no prefix ancestors are found.
  function findPrefix(el) {
    var parts = [];
    var node = el.parentElement;
    while (node && node !== root) {
      var p = node.getAttribute("data-tether-prefix");
      if (p) parts.push(p);
      node = node.parentElement;
    }
    if (parts.length === 0) return "";
    parts.reverse();
    return parts.join(".");
  }

  function bindEventType(domEvent, dataAttr) {
    root.addEventListener(domEvent, function (e) {
      var target = e.target.closest("[data-" + dataAttr + "]");
      if (!target) return;

      var action = target.getAttribute("data-" + dataAttr);
      if (!action) return;

      // Auto-prefix: if the element is inside a data-tether-prefix
      // container, prepend the prefix so components can use bare
      // action names (e.g. "send") while the server routes them via
      // the full prefixed name (e.g. "shoutbox.send").
      var prefix = findPrefix(target);
      if (prefix && action.indexOf(prefix + ".") !== 0) {
        action = prefix + "." + action;
      }

      // Show a confirmation dialog if the element requests one.
      var confirmMsg = target.getAttribute("data-tether-confirm");
      if (confirmMsg && !window.confirm(confirmMsg)) return;

      // Apply optimistic signal changes before sending the event.
      // The server's response overwrites these via applySignals.
      var optSet = target.getAttribute("data-tether-optimistic");
      if (optSet) {
        var idx = optSet.indexOf(" ");
        var key = idx === -1 ? optSet : optSet.substring(0, idx);
        var val = parseSignalValue(idx === -1 ? "true" : optSet.substring(idx + 1));
        Tether.setSignal(key, val);
      }
      var optToggle = target.getAttribute("data-tether-optimistic-toggle");
      if (optToggle) {
        Tether.setSignal(optToggle, !isTruthy(Tether.signals[optToggle]));
      }

      // Prevent default for submit events and reset the form after
      // sending so the input fields clear. The server re-renders with
      // empty values but the form isn't inside a Dynamic key, so the
      // client needs to clear it locally.
      if (domEvent === "submit" || target.hasAttribute("data-tether-prevent-default")) {
        e.preventDefault();
      }

      // Client-side validation: if the form contains fields with
      // tether validation attributes, check them before sending.
      // Uses the browser's native constraint validation API.
      if (domEvent === "submit" && !validateForm(target)) return;

      var data = {};

      // Collect custom data attributes (data-tether-data-*)
      for (var j = 0; j < target.attributes.length; j++) {
        var attr = target.attributes[j];
        if (attr.name.indexOf(eventDataPrefix) === 0) {
          var key = attr.name.substring(eventDataPrefix.length);
          data[key] = attr.value;
        }
      }

      // Collect current values from elements matching data-tether-collect.
      // Keyed by the element's name or id so the server can read them
      // by field name without the caller needing a form wrapper.
      var collectSelector = target.getAttribute("data-tether-collect");
      if (collectSelector) {
        safeQueryAll(document, collectSelector).forEach(function (el) {
          var key = el.name || el.id;
          if (!key) return;
          if (el.type === "checkbox" || el.type === "radio") {
            data[key] = el.checked ? "true" : "false";
          } else {
            data[key] = el.value || "";
          }
        });
      }

      // Collect selected item IDs from a selectable container.
      var collectSelected = target.getAttribute("data-tether-collect-selected");
      if (collectSelected) {
        var ids = [];
        safeQueryAll(document, collectSelected + " .tether-selected").forEach(function (el) {
          var id = el.getAttribute("data-tether-data-id");
          if (id) ids.push(id);
        });
        data.selected = ids.join(",");
      }

      // Collect event-specific data
      switch (domEvent) {
        case "input":
        case "change":
          data.value = target.value || "";
          // Checkboxes and radios always report their value attribute
          // (default "on"), which doesn't tell the server whether the
          // control is checked or unchecked. Send the checked state so
          // the server can distinguish between the two.
          if (target.type === "checkbox" || target.type === "radio") {
            data.checked = target.checked ? "true" : "false";
          }
          break;

        case "keydown":
          // If data-tether-key is set, only send the event if it matches.
          var filter = target.getAttribute("data-tether-key");
          if (filter && filter !== e.key) return;

          data.key = e.key || "";
          if (e.ctrlKey) data.ctrl = "true";
          if (e.shiftKey) data.shift = "true";
          if (e.altKey) data.alt = "true";
          if (e.metaKey) data.meta = "true";
          break;

        case "submit":
          var formData = new FormData(target);
          formData.forEach(function (value, key) {
            if (data[key]) {
              data[key] += "," + value;
            } else {
              data[key] = value;
            }
          });
          break;

        case "paste":
          var clipData = (e.clipboardData || window.clipboardData);
          data.value = clipData ? clipData.getData("text") : "";
          break;
      }

      // Debounce input events
      if (domEvent === "input") {
        var delay = parseInt(target.getAttribute("data-tether-debounce")) || defaultDebounce;
        var timerKey = dataAttr + ":" + action;

        clearTimeout(debounceTimers[timerKey]);
        debounceTimers[timerKey] = setTimeout(function () {
          sendEvent(domEvent, action, data);
        }, delay);
        return;
      }

      // Throttle if configured
      var throttle = parseInt(target.getAttribute("data-tether-throttle"));
      if (throttle > 0) {
        var throttleKey = "throttle:" + dataAttr + ":" + action;
        if (debounceTimers[throttleKey]) return;
        debounceTimers[throttleKey] = setTimeout(function () {
          delete debounceTimers[throttleKey];
        }, throttle);
      }

      var eid = sendEvent(domEvent, action, data);
      if (eid) disablePending(target, eid);

      // Reset form fields after submit only when explicitly requested.
      // In a server-driven framework the server controls field values
      // via the re-render - auto-resetting races the server's state.
      if (domEvent === "submit" && target.hasAttribute("data-tether-reset")) {
        target.reset();
      }
    }, domEvent === "focus" || domEvent === "blur");
  }

  function sendEvent(type, action, data) {
    if (devMode) {
      console.log("tether: event", {type: type, action: action, data: data});
    }
    var id = String(++eventCounter);
    var payload = JSON.stringify({type: type, action: action, data: data, event_id: id});

    if (connectionMode === "fetch") {
      var url = location.protocol + "//" + location.host + endpoint;
      fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: payload
      }).then(function (resp) {
        if (!resp.ok) { restorePending(id); return; }
        return resp.json();
      }).then(function (msg) {
        if (msg) applyMessage(msg);
      }).catch(function (err) {
        reportError("fetch", "page event failed: " + err);
        restorePending(id);
      });
      return id;
    }

    if (connectionMode === "sse") {
      var url = location.protocol + "//" + location.host + endpoint;
      fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Tether-Session": sessionID
        },
        body: payload
      }).then(function (resp) {
        // Restore loading state on non-2xx responses so the button
        // does not stay permanently disabled.
        if (!resp.ok) restorePending(id);
      }).catch(function (err) {
        reportError("fetch", "event POST failed: " + err, true);
        restorePending(id);
        if (backgroundSync) queueFailedEvent(payload);
      });
      return id;
    }

    if (!ws || ws.readyState !== WebSocket.OPEN) {
      if (devMode) {
        console.warn("tether: ws not open", "readyState", ws ? ws.readyState : "null", "connectionMode", connectionMode);
      }
      return null;
    }
    ws.send(payload);
    if (devMode) {
      console.log("tether: ws.send", action);
    }
    return id;
  }

  // --- Multi-select ---
  //
  // Containers with data-tether-selectable enable click, ctrl+click,
  // and shift+click selection on children that have data-tether-data-id.
  // Selection is purely client-side via the tether-selected CSS class.
  // Use data-tether-collect-selected on an action button to gather IDs.

  var lastSelected = null;

  function handleSelectable(e) {
    var container = e.target.closest("[data-tether-selectable]");
    if (!container) return;

    var item = e.target.closest("[data-tether-data-id]");
    if (!item || !container.contains(item)) return;

    var items = container.querySelectorAll("[data-tether-data-id]");

    if (e.shiftKey && lastSelected) {
      // Range select: from lastSelected to this item.
      var start = -1, end = -1;
      for (var i = 0; i < items.length; i++) {
        if (items[i] === lastSelected) start = i;
        if (items[i] === item) end = i;
      }
      if (start > -1 && end > -1) {
        var lo = Math.min(start, end);
        var hi = Math.max(start, end);
        for (var i = 0; i < items.length; i++) {
          items[i].classList.toggle("tether-selected", i >= lo && i <= hi);
        }
        trackClientClasses(item, ["tether-selected"]);
      }
    } else if (e.ctrlKey || e.metaKey) {
      // Toggle this item.
      item.classList.toggle("tether-selected");
      trackClientClasses(item, ["tether-selected"]);
    } else {
      // Single select: deselect all, select this one.
      for (var i = 0; i < items.length; i++) {
        items[i].classList.remove("tether-selected");
      }
      item.classList.add("tether-selected");
      trackClientClasses(item, ["tether-selected"]);
    }

    lastSelected = item;
  }

  // --- Client-side validation ---
  //
  // Fields with data-tether-required, data-tether-minlength,
  // data-tether-maxlength, or data-tether-pattern get native HTML
  // validation attributes applied. The browser handles the UI
  // (tooltip, red outline). validateForm checks validity before
  // the event is sent.

  function applyValidation(container) {
    var fields = container.querySelectorAll(
      "[data-tether-required], [data-tether-minlength], [data-tether-maxlength], [data-tether-pattern]"
    );
    for (var i = 0; i < fields.length; i++) {
      var el = fields[i];
      var req = el.getAttribute("data-tether-required");
      if (req !== null) {
        el.required = true;
        if (req) el.title = req;
      }
      var ml = el.getAttribute("data-tether-minlength");
      if (ml) {
        var idx = ml.indexOf(" ");
        el.minLength = parseInt(idx === -1 ? ml : ml.substring(0, idx));
        if (idx !== -1) el.title = ml.substring(idx + 1);
      }
      var xl = el.getAttribute("data-tether-maxlength");
      if (xl) {
        var idx = xl.indexOf(" ");
        el.maxLength = parseInt(idx === -1 ? xl : xl.substring(0, idx));
        if (idx !== -1) el.title = xl.substring(idx + 1);
      }
      var pat = el.getAttribute("data-tether-pattern");
      if (pat) {
        var idx = pat.indexOf(" ");
        el.pattern = idx === -1 ? pat : pat.substring(0, idx);
        if (idx !== -1) el.title = pat.substring(idx + 1);
      }
    }
  }

  function validateForm(form) {
    if (typeof form.checkValidity !== "function") return true;
    if (form.checkValidity()) return true;
    form.reportValidity();
    return false;
  }

  // --- Content editable ---
  //
  // Elements with data-tether-editable="action" forward their text
  // content to the server on blur. The element must have
  // contenteditable="true" set in the HTML.

  function bindEditables(container) {
    var els = container.querySelectorAll("[data-tether-editable]");
    for (var i = 0; i < els.length; i++) {
      setupEditable(els[i]);
    }
  }

  function setupEditable(el) {
    if (el.hasAttribute("data-tether-editable-bound")) return;
    el.setAttribute("data-tether-editable-bound", "");
    el.setAttribute("contenteditable", "true");

    el.addEventListener("blur", function () {
      var action = el.getAttribute("data-tether-editable");
      if (!action) return;

      var prefix = findPrefix(el);
      if (prefix && action.indexOf(prefix + ".") !== 0) {
        action = prefix + "." + action;
      }

      var data = { value: el.textContent || "" };
      sendEvent("blur", action, data);
    });
  }

  // --- Client-side toggles ---
  //
  // Toggle directives run entirely in the browser. The server never
  // learns about them. When a server morph arrives, the Idiomorph
  // beforeNodeMorphed hook (see morphCallbacks above) copies the
  // client-managed state onto the incoming node so it survives.

  function handleToggles(e) {
    var trigger = e.target.closest("[data-tether-toggle-class], [data-tether-toggle-attr]");
    if (!trigger) return;

    var targetSelector = trigger.getAttribute("data-tether-toggle-target");
    var target = targetSelector ? safeQuery(document, targetSelector) : trigger;
    if (!target) return;

    var toggleClass = trigger.getAttribute("data-tether-toggle-class");
    if (toggleClass) {
      var classes = toggleClass.split(/\s+/);
      for (var i = 0; i < classes.length; i++) {
        if (classes[i]) target.classList.toggle(classes[i]);
      }
      trackClientClasses(target, classes);
    }

    var toggleAttr = trigger.getAttribute("data-tether-toggle-attr");
    if (toggleAttr) {
      if (target.hasAttribute(toggleAttr)) {
        target.removeAttribute(toggleAttr);
      } else {
        target.setAttribute(toggleAttr, "");
      }
      trackClientAttrs(target, toggleAttr);
    }
  }

  function trackClientClasses(el, classNames) {
    var tracked = el.getAttribute("data-tether-client-classes") || "";
    var set = tracked ? tracked.split(/\s+/) : [];
    for (var i = 0; i < classNames.length; i++) {
      if (classNames[i] && set.indexOf(classNames[i]) === -1) {
        set.push(classNames[i]);
      }
    }
    el.setAttribute("data-tether-client-classes", set.join(" "));
  }

  function handleFocusTrap(e) {
    if (e.key !== "Tab") return;

    var container = e.target.closest("[data-tether-focus-trap]");
    if (!container) return;

    var focusables = container.querySelectorAll('button, [href], input, select, textarea, [role="button"], [role="link"], [tabindex]:not([tabindex="-1"])');
    if (focusables.length === 0) return;

    var first = focusables[0];
    var last = focusables[focusables.length - 1];

    if (e.shiftKey) {
      if (document.activeElement === first) {
        last.focus();
        e.preventDefault();
      }
    } else {
      if (document.activeElement === last) {
        first.focus();
        e.preventDefault();
      }
    }
  }

  function trackClientAttrs(el, attrName) {
    var tracked = el.getAttribute("data-tether-client-attrs") || "";
    var set = tracked ? tracked.split(/\s+/) : [];
    if (attrName && set.indexOf(attrName) === -1) {
      set.push(attrName);
    }
    el.setAttribute("data-tether-client-attrs", set.join(" "));
  }

  // --- Client-side action feedback ---
  //
  // flashFeedback provides temporary visual feedback on an element
  // after a client-side action succeeds. Two mechanisms:
  //
  //   data-tether-flash-text="Copied!"  - swaps textContent temporarily
  //   data-tether-flash-class="copied"  - adds a CSS class temporarily
  //
  // Both revert after flashDuration (default 2s). The "tether-flashed"
  // class is always added so developers can style any flashed element.
  // Called by handleClipboard and any future client-side action.

  function flashFeedback(el) {
    var flashText = el.getAttribute("data-tether-flash-text");
    var flashClass = el.getAttribute("data-tether-flash-class");
    if (!flashText && !flashClass) return;

    var duration = (Tether.config && Tether.config.flashDuration) || 2000;
    var originalText;

    if (flashText) {
      originalText = el.textContent;
      el.textContent = flashText;
    }
    if (flashClass) {
      el.classList.add(flashClass);
    }
    el.classList.add("tether-flashed");

    setTimeout(function() {
      if (flashText) el.textContent = originalText;
      if (flashClass) el.classList.remove(flashClass);
      el.classList.remove("tether-flashed");
    }, duration);
  }

  // --- Clipboard ---
  //
  // Elements with data-tether-copy="<selector>" copy the text content
  // of the matched element to the clipboard on click. No server
  // round-trip. Calls flashFeedback on the trigger for visual
  // confirmation.

  function handleClipboard(e) {
    var trigger = e.target.closest("[data-tether-copy]");
    if (!trigger) return;

    var selector = trigger.getAttribute("data-tether-copy");
    var source = safeQuery(document, selector);
    if (!source) return;

    var text = source.value !== undefined && source.value !== "" ? source.value : source.textContent;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function() {
        flashFeedback(trigger);
      });
    }
  }

  // --- Client-side scroll ---
  //
  // Elements with data-tether-scroll-to="<selector>" scroll the
  // matched element into view on click. No server round-trip.

  function handleScrollTo(e) {
    var trigger = e.target.closest("[data-tether-scroll-to]");
    if (!trigger) return;

    var selector = trigger.getAttribute("data-tether-scroll-to");
    var target = safeQuery(document, selector);
    if (target) target.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }

  // --- Global hotkeys ---
  //
  // Elements with data-tether-hotkey="<combo> <action>" register
  // global keyboard shortcuts. The combo and action are space-
  // separated in the attribute value. One hotkey per element.
  //
  // The runtime builds a registry (combo → {action, element}) on
  // init and rebuilds it after each morph. Keydown lookups are O(1)
  // with no CSS selector queries, avoiding the selector-injection
  // issues that arise from putting key names in attribute names.

  var hotkeyRegistry = {};

  function buildHotkeyRegistry() {
    hotkeyRegistry = {};
    if (!root) return;
    var els = root.querySelectorAll("[data-tether-hotkey]");
    for (var i = 0; i < els.length; i++) {
      var val = els[i].getAttribute("data-tether-hotkey");
      var spaceIdx = val.indexOf(" ");
      if (spaceIdx === -1) continue;
      var combo = val.substring(0, spaceIdx);
      var action = val.substring(spaceIdx + 1);
      hotkeyRegistry[combo] = { action: action, el: els[i] };
    }
  }

  function handleHotkeys(e) {
    var parts = [];
    if (e.ctrlKey || e.metaKey) parts.push("ctrl");
    if (e.shiftKey) parts.push("shift");
    if (e.altKey) parts.push("alt");

    var key = e.key.toLowerCase();
    if (key === " ") key = "space";
    if (key !== "control" && key !== "shift" && key !== "alt" && key !== "meta") {
      parts.push(key);
    }
    if (parts.length === 0) return;

    var combo = parts.join("-");
    var entry = hotkeyRegistry[combo];
    if (!entry) return;

    e.preventDefault();

    var action = entry.action;
    var prefix = findPrefix(entry.el);
    if (prefix && action.indexOf(prefix + ".") !== 0) {
      action = prefix + "." + action;
    }

    sendEvent("hotkey", action, { combo: combo });
  }

  // --- Client-side signal actions ---
  //
  // Signal actions let developers toggle or set signal values without
  // a server round-trip. All signal bindings (BindShow, BindClass,
  // BindText, etc.) react instantly. The server can override any
  // client-set signal via Session.Signal at any time.

  function handleSignalActions(e) {
    var toggle = e.target.closest("[data-tether-toggle-signal]");
    if (toggle) {
      var key = toggle.getAttribute("data-tether-toggle-signal");
      var next = !isTruthy(Tether.signals[key]);
      Tether.signals[key] = next;
      updateSignalBindings(key, next);
      return;
    }

    var setter = e.target.closest("[data-tether-set-signal]");
    if (setter) {
      var raw = setter.getAttribute("data-tether-set-signal");
      var idx = raw.indexOf(" ");
      var key = idx === -1 ? raw : raw.substring(0, idx);
      var value = parseSignalValue(idx === -1 ? "" : raw.substring(idx + 1));
      Tether.signals[key] = value;
      updateSignalBindings(key, value);
    }
  }

  // --- Client-side timers ---
  //
  // Timers tick entirely in the browser. The server controls them by
  // pushing signals: "name.running" (boolean) starts/pauses, and
  // setting "name" to a number resets the value. The element's text
  // content is automatically updated with the formatted time on each
  // tick - no BindText needed.

  var timers = {}; // keyed by timer name

  // scanTimers finds all data-tether-timer elements and registers them.
  // Called on init and after each morph so dynamically added timers are
  // picked up.
  function scanTimers() {
    var els = document.querySelectorAll("[data-tether-timer]");
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      var name = el.getAttribute("data-tether-timer");
      if (!name) continue;
      if (timers[name] && timers[name].el === el) continue;

      // Stop any existing timer with this name before re-registering
      // (element may have been replaced by a morph).
      if (timers[name] && timers[name].interval) {
        clearInterval(timers[name].interval);
      }

      var countdown = parseFloat(el.getAttribute("data-tether-timer-countdown"));
      var precision = parseInt(el.getAttribute("data-tether-timer-precision")) || 1000;
      var format = el.getAttribute("data-tether-timer-format") || "auto";
      var complete = el.getAttribute("data-tether-timer-complete") || "";

      var t = {
        el: el,
        name: name,
        down: countdown > 0,
        start: countdown > 0 ? countdown : 0,
        precision: precision,
        format: format,
        complete: complete,
        interval: null
      };

      timers[name] = t;

      // Initialise the signal value if not already set by the server.
      if (Tether.signals[name] === undefined || Tether.signals[name] === null) {
        Tether.signals[name] = t.start;
      }

      // Display the initial formatted value.
      el.textContent = formatTimer(Tether.signals[name], t);

      // If the running signal is already truthy (e.g. server pushed
      // it before the element appeared), start ticking immediately.
      if (isTruthy(Tether.signals[name + ".running"])) {
        startTimer(t);
      }
    }
  }

  // startTimer begins ticking. Each tick updates the signal value in
  // the local store and refreshes the element text. For count-down
  // timers reaching zero, the interval is cleared and an optional
  // completion event is sent back to the server.
  function startTimer(t) {
    if (t.interval) return; // already running
    t.interval = setInterval(function () {
      var step = t.precision / 1000;
      var current = typeof Tether.signals[t.name] === "number" ? Tether.signals[t.name] : 0;

      if (t.down) {
        current -= step;
        if (current <= 0) {
          current = 0;
          Tether.signals[t.name] = current;
          updateSignalBindings(t.name, current);
          t.el.textContent = formatTimer(current, t);
          stopTimer(t);
          // Fire completion event to the server.
          if (t.complete) {
            sendEvent("click", t.complete, {});
          }
          return;
        }
      } else {
        current += step;
      }

      Tether.signals[t.name] = current;
      updateSignalBindings(t.name, current);
      t.el.textContent = formatTimer(current, t);
    }, t.precision);
  }

  // stopTimer clears the interval and marks the running signal as
  // false locally so BindShow/BindHide elements react immediately.
  function stopTimer(t) {
    if (t.interval) {
      clearInterval(t.interval);
      t.interval = null;
    }
    // Clear the running signal locally so the client state stays
    // consistent even if the server does not explicitly push false.
    Tether.signals[t.name + ".running"] = false;
    updateSignalBindings(t.name + ".running", false);
  }

  // handleTimerSignal is called from applySignals when a signal key
  // matches a registered timer's control or value signal.
  function handleTimerSignal(key, value) {
    // Check for "name.running" pattern.
    if (key.endsWith(".running")) {
      var name = key.substring(0, key.length - 8);
      var t = timers[name];
      if (!t) return;
      if (isTruthy(value)) {
        startTimer(t);
      } else {
        if (t.interval) {
          clearInterval(t.interval);
          t.interval = null;
        }
      }
      return;
    }

    // Direct value set (e.g. reset to 0, or server sets a specific value).
    var t = timers[key];
    if (t && typeof value === "number") {
      t.el.textContent = formatTimer(value, t);
    }
  }

  // formatTimer renders a seconds value using the timer's format.
  function formatTimer(totalSeconds, t) {
    var neg = totalSeconds < 0;
    var s = Math.abs(totalSeconds);
    var fmt = t.format;

    if (fmt === "auto") {
      if (s < 60) fmt = "ss";
      else if (s < 3600) fmt = "mm:ss";
      else fmt = "hh:mm:ss";
    }

    var hours = Math.floor(s / 3600);
    var minutes = Math.floor((s % 3600) / 60);
    var seconds = Math.floor(s % 60);
    var frac = s - Math.floor(s);

    var result = "";

    switch (fmt) {
      case "hh:mm:ss":
        result = pad(hours) + ":" + pad(minutes) + ":" + pad(seconds);
        break;
      case "mm:ss":
        // Roll hours into minutes for mm:ss format.
        result = pad(hours * 60 + minutes) + ":" + pad(seconds);
        break;
      case "ss":
        result = String(Math.floor(s));
        break;
      case "mm:ss.S":
        result = pad(hours * 60 + minutes) + ":" + pad(seconds) + "." + Math.floor(frac * 10);
        break;
      case "mm:ss.SS":
        result = pad(hours * 60 + minutes) + ":" + pad(seconds) + "." + pad(Math.floor(frac * 100));
        break;
      default:
        // Unknown format - fall back to auto.
        if (s < 60) result = String(Math.floor(s));
        else if (s < 3600) result = pad(minutes) + ":" + pad(seconds);
        else result = pad(hours) + ":" + pad(minutes) + ":" + pad(seconds);
    }

    return neg ? "-" + result : result;
  }

  function pad(n) {
    return n < 10 ? "0" + n : String(n);
  }

  // --- PWA lifecycle events ---
  //
  // Propagate standard PWA/network events to the server so handlers can
  // pause background tasks, show banners, or manage install prompts.

  window.addEventListener("online", function () {
    sendEvent("online", "", {});
  });

  window.addEventListener("offline", function () {
    sendEvent("offline", "", {});
  });

  window.addEventListener("appinstalled", function () {
    sendEvent("appinstalled", "", {});
  });

  // --- Extension lazy loading ---
  //
  // Extension scripts are included on the initial page load when their
  // marker attribute appears in the rendered HTML. This function handles
  // the case where the marker first appears after a morph (e.g. a login
  // page transitions to a board with draggable elements). It dynamically
  // inserts the script tag so the extension loads without a page reload.

  var extensionMarkers = [
    { attr: "data-tether-upload", script: "tether-upload.js" },
    { attr: "data-tether-draggable", script: "tether-drag-and-drop.js" },
    { attr: "data-tether-sortable", script: "tether-drag-and-drop.js" },
    { attr: "data-tether-swipe", script: "tether-touch.js" },
    { attr: "data-tether-longpress", script: "tether-touch.js" }
  ];
  var loadedExtensions = {};

  function loadExtensions() {
    for (var i = 0; i < extensionMarkers.length; i++) {
      var ext = extensionMarkers[i];
      if (loadedExtensions[ext.script]) continue;
      if (!root.querySelector("[" + ext.attr + "]")) continue;
      // Check if already loaded by the initial page render.
      var scripts = document.querySelectorAll("script[src*='" + ext.script + "']");
      if (scripts.length > 0) {
        loadedExtensions[ext.script] = true;
        continue;
      }
      loadedExtensions[ext.script] = true;
      var tag = document.createElement("script");
      tag.src = "/_tether/" + ext.script + "?v=" + Date.now();
      document.body.appendChild(tag);
      if (devMode) console.log("tether: lazy-loaded extension", ext.script);
    }
  }

  // --- Extension API ---
  //
  // Expose a minimal surface for extension scripts (tether-*.js).
  // Extensions load after this file and use these to communicate with
  // the server and update client-side state.

  window.Tether.sendEvent = sendEvent;
  window.Tether.setSignal = function (key, value) {
    Tether.signals[key] = value;
    updateSignalBindings(key, value);
  };
})();
