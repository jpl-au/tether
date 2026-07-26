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
// "push", "indexeddb", "render", "compute", and "extension". If not set,
// warnings are logged to the console and silent errors remain silent.
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
  // Latest pending sample per continuous-event binding, flushed once per
  // animation frame. See the coalescing block in handleBoundEvent.
  var framePending = {};
  var leavingNodes = new Set();
  var pendingElements = {};
  // Elements whose bind.Once binding has already fired. A WeakSet keyed
  // on the element so a morph that replaces the node resets the guard.
  var firedOnce = new WeakSet();
  // Ref-counts of in-flight events per loading indicator element, so an
  // indicator shared by overlapping events clears only when the last one
  // settles (see disablePending / restorePending).
  var indicatorCounts = new WeakMap();
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
  var viewTransitions = false;
  var pendingCount = 0;
  var eventDataPrefix = "data-tether-data-";
  // Fragment content hashes for stateless auto-fragments: seeded by
  // the initial GET's island, echoed with each event, and replaced
  // wholesale from every response's hashes field. Null when the page
  // does not use auto-fragments.
  var fragmentHashes = null;

  // Computed signals. A [data-tether-computed="name|<program>"] element
  // registers a computed signal here: computedByName holds one spec per
  // name (declare a name once), computedByDep maps each input signal to
  // the specs that read it so a write finds exactly what to recompute,
  // and recomputing guards against dependency cycles during a cascade.
  // Parsed programs are cached per element in programCache.
  var computedByName = Object.create(null);
  var computedByDep = Object.create(null);
  var recomputing = Object.create(null);
  var programCache = new WeakMap();

  // --- Extension listener registries ---
  //
  // Extensions (tether-hotkey.js, tether-timer.js, ...) and app code
  // register callbacks for runtime lifecycle moments. Each listener
  // is guarded: a throwing extension must not break the core or the
  // other listeners.

  var signalListeners = [];  // fn(key, value) after a signal changes
  var updateListeners = [];  // fn(root) after a server update applies
  var addedListeners = [];   // fn(el) after a morph adds an element
  var removedListeners = []; // fn(el) before a morph removes an element

  function addListener(list, fn) {
    list.push(fn);
    return function () {
      var i = list.indexOf(fn);
      if (i !== -1) list.splice(i, 1);
    };
  }

  function notify(list, a, b) {
    for (var i = 0; i < list.length; i++) {
      try {
        list[i](a, b);
      } catch (e) {
        reportError("extension", "listener threw: " + e, false, "listener-threw");
      }
    }
  }

  // Report an error or warning to the Tether.onError callback if set.
  // Falls back to console for non-silent errors. The callback is
  // guarded - a throwing error reporter must not abort the runtime
  // work (e.g. remaining patches) that triggered the report.
  //
  // slug is a stable kebab-case identifier catalogued in
  // tether/docs/errors.md; it is added to the onError payload and, in
  // the console fallback, turned into a "see ...#slug" pointer. When an
  // element is implicated it is passed as a trailing console.error
  // argument so it is clickable and inspectable in devtools.
  function reportError(type, message, silent, slug, element) {
    if (typeof window.Tether.onError === "function") {
      try {
        window.Tether.onError({ type: type, message: message, slug: slug });
      } catch (e) {
        console.warn("tether: Tether.onError callback threw: " + e);
      }
    } else if (!silent) {
      var out = "tether: " + message;
      if (slug) out += " [" + slug + "] - see tether/docs/errors.md#" + slug;
      if (element) console.error(out, element);
      else console.warn(out);
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
      reportError("render", "invalid selector: " + selector, false, "invalid-selector");
      return null;
    }
  }

  function safeQueryAll(parent, selector) {
    try {
      return parent.querySelectorAll(selector);
    } catch (e) {
      reportError("render", "invalid selector: " + selector, false, "invalid-selector");
      return [];
    }
  }

  // Build an attribute selector with the value properly escaped.
  // Example: attrSelector("data-fluent-key", key) returns
  // '[data-fluent-key="<escaped>"]'
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
    viewTransitions = root.hasAttribute("data-tether-view-transitions");
    // Remove cloak attributes so hidden elements become visible now
    // that the runtime is ready. The server injects a style rule that
    // hides [data-tether-cloak] elements before JS loads.
    var cloaked = document.querySelectorAll("[data-tether-cloak]");
    for (var i = 0; i < cloaked.length; i++) cloaked[i].removeAttribute("data-tether-cloak");

    initViewportObserver();

    // Stateless auto-fragments: the server seeds the hash map in a
    // template island inside the root.
    var hashSeed = root.querySelector("template[data-tether-hashes]");
    if (hashSeed) {
      try {
        var seedRaw = hashSeed.content ? hashSeed.content.textContent : hashSeed.textContent;
        fragmentHashes = JSON.parse(seedRaw);
      } catch (err) {
        reportError("parse", "failed to parse fragment hash seed: " + err, false, "fragment-hash-seed-parse", hashSeed);
      }
    }

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

    // Register computed-signal declarations present in the initial render
    // (the whole document, so shell-level declarations count too). Each
    // defers until its inputs arrive via the first applySignals; the
    // bindings on them apply harmlessly against the still-empty signals.
    reapplySignals(document.documentElement);

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
        // The session ID travels in the beacon body, not the URL,
        // so it stays out of server access logs.
        navigator.sendBeacon(endpoint + "?tether=destroy", sessionID);
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
          reportError("worker", "service worker registration failed: " + err, false, "service-worker-registration-failed");
        });
    } else if (root.hasAttribute("data-tether-push-key") && "serviceWorker" in navigator) {
      // Push-only service worker: receives push events and shows
      // notifications without intercepting fetch requests or caching.
      navigator.serviceWorker.register("/_tether/tether-push-worker.js", { scope: endpoint || "/" })
        .catch(function (err) {
          reportError("worker", "push worker registration failed: " + err, false, "push-worker-registration-failed");
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

    // Client events and filtering also bind on document so triggers and
    // controls in the Layout shell behave the same as those inside root.
    document.addEventListener("click", handleEmit);
    document.addEventListener("input", handleFilter);
    document.addEventListener(CLIENT_EVENT, receiveClientEvent);
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

  // Ask the server for a one-time connect ticket. The session ID is a
  // bearer token, so it travels in a header - never in a URL where
  // access logs, proxies, and browser history would capture it. Only
  // the single-use, short-lived ticket appears in the transport URL.
  // A previous session left in sessionStorage by a page refresh rides
  // along in Tether-Replaces so the server can destroy it immediately
  // instead of waiting out its disconnect timer.
  function requestTicket(cb) {
    var url = location.protocol + "//" + location.host + endpoint + "?tether=ticket";
    var headers = {};
    if (sessionID) headers["Tether-Session"] = sessionID;
    var prev = sessionStorage.getItem(storageKey());
    if (prev && prev !== sessionID) headers["Tether-Replaces"] = prev;
    fetch(url, { method: "POST", headers: headers }).then(function (resp) {
      if (!resp.ok) throw new Error("status " + resp.status);
      return resp.text();
    }).then(cb).catch(function (err) {
      reportError("fetch", "connect ticket request failed: " + err, true, "connect-ticket-failed");
      if (root) root.setAttribute("data-tether-state", "disconnected");
      showReconnectBar();
      scheduleReconnect();
    });
  }

  function connectWS() {
    requestTicket(openWS);
  }

  function openWS(ticket) {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var url = protocol + "//" + location.host + endpoint + "?ticket=" + encodeURIComponent(ticket);

    ws = new WebSocket(url);
    // Binary frames carry CBOR payloads. ArrayBuffer delivers them
    // synchronously to onmessage (the default Blob would need an
    // async read); Tether.decode handles both strings and buffers.
    ws.binaryType = "arraybuffer";

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
      flushSendQueue();
      // Re-arm viewport triggers whose send failed while disconnected.
      observeViewportElements(root);
    };

    ws.onmessage = function (e) {
      var msg;
      try {
        msg = window.Tether.decode(e.data);
      } catch (err) {
        reportError("parse", "failed to parse WebSocket message: " + err, true, "ws-message-parse");
        return;
      }
      applyMessage(msg);
    };

    ws.onclose = function () {
      if (root) root.setAttribute("data-tether-state", "disconnected");
      showReconnectBar();
      // Events awaiting an echo will never get one on this
      // connection - restore their loading state so buttons don't
      // stay disabled forever.
      restoreAllPending();
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
    requestTicket(openSSE);
  }

  function openSSE(ticket) {
    var url = location.protocol + "//" + location.host + endpoint + "?ticket=" + encodeURIComponent(ticket);

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
      // Re-arm viewport triggers whose send failed while disconnected.
      observeViewportElements(root);
    };

    eventSource.onmessage = function (e) {
      var msg;
      try {
        msg = window.Tether.decode(e.data);
      } catch (err) {
        reportError("parse", "failed to parse SSE message: " + err, true, "sse-message-parse");
        return;
      }
      applyMessage(msg);
    };

    eventSource.onerror = function () {
      if (root) root.setAttribute("data-tether-state", "disconnected");
      showReconnectBar();
      restoreAllPending();
      // Reconnection is managed here with the same backoff loop the
      // WebSocket path uses, for two reasons. First, the browser's
      // automatic EventSource retry gives up permanently (readyState
      // CLOSED) when a reconnect attempt *fails* - e.g. a 502 from
      // the load balancer during a deploy - which would leave the
      // page stuck on "Reconnecting" forever. Second, connect
      // tickets are single-use, so a browser-driven retry of the
      // same URL would be rejected anyway. Close and start over
      // with a fresh ticket.
      if (eventSource) {
        eventSource.close();
        eventSource = null;
        scheduleReconnect();
      }
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
            reportError("push", "unsubscribe failed: " + err, false, "push-unsubscribe-failed");
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
      reportError("push", "push subscription failed: " + err, false, "push-subscribe-failed");
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
          reportError("push", "push subscription POST failed: " + err, false, "push-subscribe-post-failed");
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
      reportError("indexeddb", "failed to queue event: " + err, true, "event-queue-failed");
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
          // Discard events older than the retention window.
          if (now - events[i].ts > syncRetention) {
            deleteEventFromDB(db, keys[i]);
            continue;
          }
          // Events queued by other tabs (different session ID) are
          // not orphans - the queue is shared across tabs. Leave
          // them for their own tab (or the retention window) rather
          // than deleting another tab's pending work.
          if (events[i].sessionID !== sessionID) continue;
          replayAndDeleteEvent(db, keys[i], events[i].payload, url);
        }
      };
    }).catch(function (err) {
      reportError("indexeddb", "failed to replay queued events: " + err, true, "event-replay-failed");
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
      reportError("fetch", "event replay failed: " + err, true, "event-replay-failed");
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

      // Stateless auto-fragments: every response carries the complete
      // fresh hash map - replace, never merge.
      if (msg.hashes) {
        fragmentHashes = msg.hashes;
      }

      // Server reassigned session ID (stale client reconnection).
      if (msg.session) {
        sessionID = msg.session;
        if (root) {
          root.setAttribute("data-tether-session", sessionID);
        }
      }

      // The DOM-mutating portion: content patches first, then
      // structural morphs. Isolated into one function so it can run
      // inside a View Transition callback - startViewTransition
      // snapshots the DOM before and after this runs and cross-fades
      // between the two.
      function applyDOM() {
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
      }

      // Everything sequenced after the morph flush: effects, signal
      // reapplication, autofocus, extension hooks. This ordering is
      // preserved whether or not a View Transition wraps the mutation -
      // with transitions on, it runs once the update callback has
      // completed (the DOM is already mutated by then).
      function postFlush() {
        if (msg.url) {
          if (msg.replace) {
            history.replaceState({}, "", msg.url);
          } else if (msg.url !== location.pathname + location.search) {
            history.pushState({}, "", msg.url);
            // Server-driven navigation: pushState changes the URL but
            // does not trigger popstate, so the server never learns about
            // the new URL. Send a navigate event so OnNavigate can update
            // state and re-render for the target page. The same-URL check
            // above breaks the echo loop that would otherwise form when
            // OnNavigate re-navigates to the URL the browser is already on.
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
        if (msg.prefetch) {
          applyPrefetch(msg.prefetch);
        }
        if (msg.signals) {
          applySignals(msg.signals);
        }

        // Set focus on the designated element after all DOM updates.
        // Uses data-tether-autofocus (not data-tether-focus, which is the
        // event binding attribute for the Focus helper). Only runs when
        // the DOM actually changed and the element isn't already focused,
        // so signal-only broadcasts can't steal focus from the user.
        if (msg.patches || msg.morphs) {
          var focusEl = root.querySelector("[data-tether-autofocus]");
          if (focusEl && focusEl !== document.activeElement) focusEl.focus();
        }

        // Attach listeners for any event type this update introduced.
        // Delegation already covers new elements carrying a type that
        // is bound; a type appearing for the first time needs its own
        // listener, exactly like a lazily loaded extension. Only the
        // morphed tree can have changed, so the scan starts at root.
        discoverEvents(root);

        // Lazy-load extension scripts when their marker attributes first
        // appear in the DOM after a morph. This eliminates the need for
        // hidden marker elements on the initial page.
        loadExtensions();
        applyValidation(root);
        bindEditables(root);

        // Notify extensions that the DOM has been updated so they can
        // re-scan for new elements (e.g. hotkeys or timers added by a
        // morph). Both channels fire: registered onUpdate listeners and
        // the tether:update DOM event.
        notify(updateListeners, root);
        document.dispatchEvent(new CustomEvent("tether:update", { detail: { root: root } }));
      }

      // Opt-in View Transitions: wrap only the DOM mutation, keeping all
      // post-morph work after the update callback resolves. Skipped when
      // disabled, unsupported, when the update mutates no DOM (a
      // signal-only broadcast), or when the user prefers reduced motion -
      // in which case behaviour is byte-for-byte identical to before.
      var useTransition = viewTransitions &&
        (msg.patches || msg.morphs) &&
        typeof document.startViewTransition === "function" &&
        !(window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches);
      if (useTransition) {
        var transition = document.startViewTransition(applyDOM);
        transition.updateCallbackDone.then(postFlush, postFlush);
      } else {
        applyDOM();
        postFlush();
      }
    });
  }

  // Track URLs already hinted to the browser so a repeated Prefetch
  // effect never appends a duplicate rule or link for the page's life.
  var prefetchedURLs = {};

  // applyPrefetch hints the browser to speculatively fetch likely-next
  // URLs. Where the Speculation Rules API is available it appends a
  // <script type="speculationrules"> element whose body is declarative
  // JSON data (never executed - no eval, no innerHTML), so tether's
  // no-eval posture is intact. Otherwise it falls back to one
  // <link rel="prefetch"> per URL. Each URL is emitted at most once.
  function applyPrefetch(urls) {
    var fresh = [];
    for (var i = 0; i < urls.length; i++) {
      var url = urls[i];
      if (url && !prefetchedURLs[url]) {
        prefetchedURLs[url] = true;
        fresh.push(url);
      }
    }
    if (fresh.length === 0) return;

    if (typeof HTMLScriptElement !== "undefined" &&
        HTMLScriptElement.supports && HTMLScriptElement.supports("speculationrules")) {
      var script = document.createElement("script");
      script.type = "speculationrules";
      script.textContent = JSON.stringify({ prefetch: [{ urls: fresh }] });
      document.head.appendChild(script);
    } else {
      for (var j = 0; j < fresh.length; j++) {
        var link = document.createElement("link");
        link.rel = "prefetch";
        link.href = fresh[j];
        document.head.appendChild(link);
      }
    }
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
      if (entry.indicator) acquireIndicator(entry.indicator);
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
    if (entry.indicator) releaseIndicator(entry.indicator);

    if (--pendingCount === 0 && root) {
      root.classList.remove("tether-loading");
    }

    delete pendingElements[eventID];
  }

  // acquireIndicator / releaseIndicator ref-count the tether-pending
  // class on a loading indicator. Two elements can point their
  // bind.Indicator at the same selector (or one element can fire twice
  // before the first settles); without ref-counting, the first event to
  // finish would clear the indicator while a later event is still in
  // flight. The class is added on the first acquire and removed only on
  // the last release.
  function acquireIndicator(el) {
    var n = (indicatorCounts.get(el) || 0) + 1;
    indicatorCounts.set(el, n);
    if (n === 1) el.classList.add("tether-pending");
  }

  function releaseIndicator(el) {
    var n = (indicatorCounts.get(el) || 0) - 1;
    if (n <= 0) {
      indicatorCounts.delete(el);
      el.classList.remove("tether-pending");
    } else {
      indicatorCounts.set(el, n);
    }
  }

  // restoreAllPending clears every outstanding loading state. Called
  // when the transport drops: the echoes those states are waiting for
  // will never arrive on this connection, and after a reconnect the
  // server re-sends full state anyway.
  function restoreAllPending() {
    for (var eventID in pendingElements) {
      restorePending(eventID);
    }
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
      notify(addedListeners, newNode);
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
      notify(removedListeners, oldNode);
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

  // Nodes whose viewport trigger already fired. Morphs reuse DOM
  // nodes, so without this the post-morph re-observe would re-fire
  // the trigger for an element that never left the viewport (e.g.
  // infinite scroll re-sending "load more" on every broadcast). A
  // genuinely new element is a new node and arms normally.
  var firedViewport = new WeakSet();

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
          // Only consume the trigger if the event was actually sent.
          // When the transport isn't open yet, stay observed so the
          // trigger fires once the connection is up instead of being
          // lost permanently.
          if (sendEvent("viewport", action, data) === null) continue;
          firedViewport.add(el);
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
      reobserveViewport(els[i]);
    }
    // The container itself might be a viewport element (e.g. a sentinel
    // div inserted by a morph).
    if (container.hasAttribute && container.hasAttribute("data-tether-viewport")) {
      reobserveViewport(container);
    }
  }

  // Unobserve-then-observe forces the IntersectionObserver to deliver
  // the element's current intersection state again. Without it, a
  // trigger whose send failed while disconnected would sit silently
  // in the viewport forever - the observer only fires on changes.
  function reobserveViewport(el) {
    if (firedViewport.has(el)) return;
    viewportObserver.unobserve(el);
    viewportObserver.observe(el);
  }

  // --- Patching and morphing ---

  // isEditingActiveElement reports whether the user is currently typing in
  // a form field or contenteditable region. Used to arm ignoreActiveValue
  // only when there is in-progress text to protect.
  function isEditingActiveElement() {
    var el = document.activeElement;
    if (!el || el === document.body) return false;
    var tag = el.tagName;
    return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || el.isContentEditable === true;
  }

  // morphOptions builds the idiomorph config shared by every morph.
  //
  // ignoreActiveValue keeps a server patch from overwriting the value of the
  // input the user is actively typing in: idiomorph skips the value attribute
  // and live value of document.activeElement, so in-progress text survives a
  // re-render that would otherwise reset it. It is armed only while a form
  // field or contenteditable is focused - idiomorph's ignoreActiveValue also
  // skips morphing the *children* of the active element, so leaving it on
  // unconditionally would freeze the subtree of whatever is focused during a
  // morph (a clicked link, a button), breaking client-side navigation and any
  // update that re-renders around the focused control.
  //
  // restoreFocus is disabled. When on, idiomorph re-focuses the active
  // input/textarea by id after every morph and reapplies its selection
  // range, firing focus/focusin/select events. Tether manages focus itself
  // (data-tether-autofocus, and in-place morphs keep the caret without
  // help), so idiomorph's re-focus is redundant and its extra events
  // perturb focus-sensitive behaviour.
  //
  // A fresh object is returned each call so idiomorph never mutates shared
  // state; pass extra keys (e.g. morphStyle) to merge them in.
  function morphOptions(extra) {
    var opts = {
      callbacks: morphCallbacks,
      ignoreActiveValue: isEditingActiveElement(),
      restoreFocus: false
    };
    if (extra) {
      for (var k in extra) opts[k] = extra[k];
    }
    return opts;
  }

  function applyPatch(patch) {
    var el = safeQuery(document, attrSelector("data-fluent-key", patch.key));
    if (!el) return;

    if (devMode) {
      console.log("tether: patch", patch.key);
    }

    var template = document.createElement("template");
    template.innerHTML = patch.html;
    if (template.content.childElementCount > 1) {
      reportError("render", "patch for key '" + patch.key + "' contains multiple root elements; only the first will be used", false, "multiple-root-elements", el);
    }
    var newEl = template.content.firstElementChild;
    if (!newEl) return;

    Idiomorph.morph(el, newEl, morphOptions());
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
        Idiomorph.morph(root, morph.html, morphOptions({morphStyle: "innerHTML"}));
      }
    } else {
      // Scoped morph targets a keyed container.
      var template = document.createElement("template");
      template.innerHTML = morph.html;
      var el = safeQuery(document, attrSelector("data-fluent-key", morph.key));
      if (template.content.childElementCount > 1) {
        reportError("render", "morph for key '" + morph.key + "' contains multiple root elements; only the first will be used", false, "multiple-root-elements", el);
      }
      var newEl = template.content.firstElementChild;
      if (!newEl) return;
      if (el) {
        Idiomorph.morph(el, newEl, morphOptions());
      }
    }
  }

  // --- Signals ---
  //
  // Signals are reactive key/value pairs pushed by the server. Elements
  // bind to signals via data-tether-bind-* attributes. When a signal
  // changes, all bound elements update instantly - no render, no diff.
  // Signal values are stored in Tether.signals so JS hooks can read them.

  // setSignal is the single write path for signal values - server
  // pushes, client-side signal actions, and extension writes all land
  // here, so bindings and onSignalChange listeners always agree.
  function setSignal(key, value) {
    window.Tether.signals[key] = value;
    updateSignalBindings(key, value);
    notify(signalListeners, key, value);
    // A write may feed one or more computed signals. Recompute them last,
    // once the raw write has landed, so their programs read the new value.
    recomputeDependents(key);
  }

  function applySignals(updates) {
    for (var key in updates) {
      setSignal(key, updates[key]);
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

    // Conditional bindings (show-when / hide-when / class-when) compile
    // to postfix programs and run through the shared VM. A program may
    // read several signals, so on any signal change re-evaluate them all.
    applyConditionals(document);
  }

  // --- Computed signal VM ---
  //
  // Computed signals and the conditional bindings share one evaluator.
  // The server compiles an infix expression to a postfix program - an
  // array of [opcode, arg] cells - and this stack machine runs it. There
  // is no eval and no Function: the opcode set is fixed and closed, so a
  // strict script-src 'self' CSP holds. Opcodes: 0 push signal value,
  // 1 push number/boolean literal, 2 push string literal, 3 binary op,
  // 4 unary op.
  function runProgram(program, signals) {
    var stack = [];
    for (var i = 0; i < program.length; i++) {
      var op = program[i][0], arg = program[i][1];
      if (op === 0) stack.push(signals[arg]);
      else if (op === 1 || op === 2) stack.push(arg);
      else if (op === 3) { var b = stack.pop(), a = stack.pop(); stack.push(binaryOp(arg, a, b)); }
      else stack.push(unaryOp(arg, stack.pop()));
    }
    return stack.pop();
  }

  // binaryOp applies one binary operator. "+" doubles as string concat
  // when either operand is a string; ordered comparisons are numeric and
  // yield false on non-numbers.
  function binaryOp(op, a, b) {
    if (op === "+") {
      if (typeof a === "string" || typeof b === "string") {
        return (a == null ? "" : String(a)) + (b == null ? "" : String(b));
      }
      return Number(a) + Number(b);
    }
    if (op === "-") return Number(a) - Number(b);
    if (op === "*") return Number(a) * Number(b);
    if (op === "/") return Number(a) / Number(b);
    if (op === "%") return Number(a) % Number(b);
    if (op === "==") return a == b;
    if (op === "!=") return a != b;
    if (op === "and") return isTruthy(a) && isTruthy(b);
    if (op === "or") return isTruthy(a) || isTruthy(b);
    var x = Number(a), y = Number(b);
    if (isNaN(x) || isNaN(y)) return false;
    if (op === ">") return x > y;
    if (op === ">=") return x >= y;
    if (op === "<") return x < y;
    return x <= y;
  }

  // unaryOp applies not / neg / len. len is string or array length;
  // anything else has length 0.
  function unaryOp(op, v) {
    if (op === "not") return !isTruthy(v);
    if (op === "neg") return -Number(v);
    if (typeof v === "string" || Array.isArray(v)) return v.length;
    return 0;
  }

  // parseProgram decodes a program's JSON once and caches it on the
  // element, re-parsing only if the attribute text changes across morphs.
  function parseProgram(el, raw) {
    var cached = programCache.get(el);
    if (cached && cached.raw === raw) return cached.program;
    var program;
    try {
      program = JSON.parse(raw);
    } catch (e) {
      reportError("parse", "invalid computed program: " + e, false, "invalid-computed-program", el);
      return null;
    }
    programCache.set(el, { raw: raw, program: program });
    return program;
  }

  // depsReady is true when every signal a program reads is present. A
  // binding stays untouched until its inputs arrive, so it never reflects
  // a half-populated state.
  function depsReady(program) {
    for (var i = 0; i < program.length; i++) {
      if (program[i][0] === 0 && !window.Tether.signals.hasOwnProperty(program[i][1])) return false;
    }
    return true;
  }

  // programDeps lists the distinct signals a program reads (its opcode-0
  // cells). Dependencies are derived here rather than shipped separately.
  function programDeps(program) {
    var deps = [];
    for (var i = 0; i < program.length; i++) {
      if (program[i][0] === 0 && deps.indexOf(program[i][1]) === -1) deps.push(program[i][1]);
    }
    return deps;
  }

  // registerComputed wires a "name|<program>" declaration into the
  // dependency map. Re-declaring a name (e.g. after a morph re-adds the
  // element) refreshes the wiring in place rather than duplicating it.
  function registerComputed(el) {
    var raw = el.getAttribute("data-tether-computed");
    if (!raw) return;
    var bar = raw.indexOf("|");
    if (bar === -1) return;
    var name = raw.slice(0, bar);
    var program = parseProgram(el, raw.slice(bar + 1));
    if (!program) return;
    var spec = { name: name, program: program, deps: programDeps(program) };
    var prev = computedByName[name];
    if (prev) {
      for (var i = 0; i < prev.deps.length; i++) {
        var list = computedByDep[prev.deps[i]];
        var at = list ? list.indexOf(prev) : -1;
        if (at !== -1) list.splice(at, 1);
      }
    }
    computedByName[name] = spec;
    for (var j = 0; j < spec.deps.length; j++) {
      (computedByDep[spec.deps[j]] || (computedByDep[spec.deps[j]] = [])).push(spec);
    }
    evaluateComputed(spec);
  }

  // evaluateComputed runs a spec only once all its inputs are present,
  // deferring otherwise until a dependency first arrives.
  function evaluateComputed(spec) {
    if (depsReady(spec.program)) recomputeComputed(spec);
  }

  // recomputeComputed runs the program and publishes the result under the
  // computed's name via setSignal - which drives its bindings and cascades
  // to any computed that reads it. The recomputing guard aborts a branch
  // that re-enters the same name (a cycle) and reports it, never throwing.
  function recomputeComputed(spec) {
    if (recomputing[spec.name]) {
      reportError("compute", "cycle detected recomputing signal " + spec.name, false, "computed-cycle");
      return;
    }
    recomputing[spec.name] = true;
    try {
      setSignal(spec.name, runProgram(spec.program, window.Tether.signals));
    } finally {
      delete recomputing[spec.name];
    }
  }

  // recomputeDependents re-evaluates every computed that reads key, called
  // from setSignal after each write.
  function recomputeDependents(key) {
    var specs = computedByDep[key];
    if (!specs) return;
    for (var i = 0; i < specs.length; i++) evaluateComputed(specs[i]);
  }

  // applyConditional evaluates one show-when / hide-when / class-when
  // binding against the current signals and applies its DOM effect. All
  // three compile to the same postfix programs as computed signals.
  function applyConditional(el) {
    var raw = el.getAttribute("data-tether-bind-show-when");
    if (raw != null) { toggleWhen(el, raw, false); return; }
    raw = el.getAttribute("data-tether-bind-hide-when");
    if (raw != null) { toggleWhen(el, raw, true); return; }
    raw = el.getAttribute("data-tether-bind-class-when");
    if (raw != null) {
      var bar = raw.indexOf("|");
      if (bar === -1) return;
      var program = parseProgram(el, raw.slice(bar + 1));
      if (program && depsReady(program)) {
        el.classList.toggle(raw.slice(0, bar), isTruthy(runProgram(program, window.Tether.signals)));
      }
    }
  }

  // toggleWhen drives display for a show-when (invert false) or hide-when
  // (invert true) binding.
  function toggleWhen(el, raw, invert) {
    var program = parseProgram(el, raw);
    if (!program || !depsReady(program)) return;
    var on = isTruthy(runProgram(program, window.Tether.signals));
    el.style.display = (invert ? !on : on) ? "" : "none";
  }

  function applyConditionals(scope) {
    var els = scope.querySelectorAll(
      "[data-tether-bind-show-when],[data-tether-bind-hide-when],[data-tether-bind-class-when]");
    for (var i = 0; i < els.length; i++) applyConditional(els[i]);
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
      "[data-tether-bind-class],[data-tether-bind-attr],[data-tether-bind-value]," +
      "[data-tether-bind-show-when],[data-tether-bind-hide-when],[data-tether-bind-class-when]," +
      "[data-tether-computed]"
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

    // Register a computed declaration so it recomputes on future writes,
    // and re-evaluate any conditional binding against the current signals
    // so a freshly morphed element reflects the latest state.
    if (el.hasAttribute("data-tether-computed")) registerComputed(el);
    applyConditional(el);
  }

  // --- Event delegation ---

  // Every server event binding renders data-tether-event-<domEvent>.
  // The prefix is what tells an event binding apart from a control
  // attribute, so the set of event types is open: discoverEvents finds
  // the names a page actually uses, at load and after every update.
  // Keep in step with eventAttr in bind/apply.go.
  var eventAttrPrefix = "data-tether-event-";

  // Event types already listening, so discovery is idempotent. Null
  // prototype so an event named "toString" cannot read as bound.
  var boundTypes = Object.create(null);
  var scopedTypes = Object.create(null);

  // Continuous events fire far faster than a server round trip can
  // absorb, so they coalesce to the latest sample once per frame.
  // bind.Throttle, bind.Debounce and bind.Delay all override this.
  var continuousEvents = {
    mousemove: true, pointermove: true, touchmove: true, drag: true,
    dragover: true, scroll: true, wheel: true, resize: true
  };

  // discoverEvents attaches listeners for every event name present in
  // el's subtree. Runs at load and after each update, so a binding
  // morphed in later is live without the server declaring it up front.
  // Template content is searched too: tether-template.js stamps that
  // markup into the page client-side, with no server round trip to
  // trigger another scan.
  function discoverEvents(el) {
    if (!el) return;
    var all = el.querySelectorAll("*");
    for (var i = -1; i < all.length; i++) {
      var node = i < 0 ? el : all[i];
      if (node.nodeType !== 1) continue;
      var attrs = node.attributes;
      for (var j = 0; j < attrs.length; j++) {
        var name = attrs[j].name;
        if (name.lastIndexOf(eventAttrPrefix, 0) !== 0) continue;
        var type = name.slice(eventAttrPrefix.length);
        if (!type) continue;
        bindEventType(type);
        if (node.hasAttribute("data-tether-outside") || node.hasAttribute("data-tether-at")) {
          bindScopedEvent(type);
        }
      }
      // Template content is a detached fragment querySelectorAll cannot
      // reach, and tether-template.js stamps that markup into the page
      // client-side with no server round trip to trigger another scan.
      // Gate on the tag: HTMLMetaElement.content reflects the attribute
      // as a string, so a bare truthiness check walks into <meta> and
      // throws, taking every binding on the page with it.
      if (node.tagName === "TEMPLATE" && node.content) discoverEvents(node.content);
    }
  }

  // listen registers handler for one DOM event in whichever phase can
  // see it. Every dispatch runs the capture path from the window down to
  // the target; only a bubbling one runs the path back up. Registering
  // both and letting each claim the events it owns means tether needs no
  // table of which events bubble: a bubbling event is handled on the way
  // up, which is the ordering bind.Stop relies on, and a non-bubbling one
  // (focus, blur, scroll, mouseenter) on the way down.
  //
  // passive:false because browsers make wheel and touch listeners
  // passive by default on document and window, which would turn
  // bind.PreventDefault into a console error.
  function listen(target, type, handler) {
    target.addEventListener(type, function (e) {
      if (e.bubbles) handler(e);
    }, { passive: false });
    target.addEventListener(type, function (e) {
      if (!e.bubbles) handler(e);
    }, { capture: true, passive: false });
  }

  // warnBindingsOutsideRoot reports event bindings the framework cannot
  // reach. Delegation is scoped to the tether root because that is the
  // subtree the server renders and diffs; a binding in the Layout shell
  // sits outside it and would never fire. Rather than widen delegation
  // to a region the diff engine never updates, say so plainly. Dev mode
  // only - one whole-document query at startup, and nothing shipped to
  // production.
  function warnBindingsOutsideRoot() {
    if (!devMode || !root) return;
    var all = document.querySelectorAll("*");
    for (var i = 0; i < all.length; i++) {
      var el = all[i];
      if (root.contains(el)) continue;
      var attrs = el.attributes;
      for (var j = 0; j < attrs.length; j++) {
        if (attrs[j].name.lastIndexOf(eventAttrPrefix, 0) !== 0) continue;
        reportError("render",
          "event binding on an element outside the tether root will never fire: " +
          attrs[j].name + '="' + attrs[j].value + '". Bindings belong inside Render, ' +
          "not in the Layout shell, which is rendered once and never diffed.",
          false, "binding-outside-root", el);
        break;
      }
    }
  }

  function bindEvents() {
    discoverEvents(root);
    warnBindingsOutsideRoot();
    // click is bound unconditionally: the framework's own root handlers
    // below need it whether or not the page carries a click binding.
    bindEventType("click");
    root.addEventListener("click", handleToggles);
    root.addEventListener("click", handleLinks);
    root.addEventListener("click", handleClipboard);
    root.addEventListener("click", handleScrollTo);
    window.addEventListener("keydown", handleFocusTrap);

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
    // Leave any href with a scheme (http:, mailto:, tel:, ...) or a
    // protocol-relative URL to the browser - only same-origin paths
    // route through the server's OnNavigate.
    if (!href || /^[a-z][a-z0-9+.-]*:/i.test(href) || href.indexOf("//") === 0) return;

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

  // bindEventType attaches the delegated listener for one DOM event.
  // Delegation is on the tether root, which is the subtree the server
  // renders and diffs. warnBindingsOutsideRoot reports anything bound
  // beyond it.
  function bindEventType(domEvent) {
    if (boundTypes[domEvent]) return;
    boundTypes[domEvent] = true;
    var dataAttr = "tether-event-" + domEvent;
    // Built once per type rather than per dispatch.
    var selector = "[" + CSS.escape(eventAttrPrefix + domEvent) + "]";

    listen(root, domEvent, function (e) {
      var t = e.target;
      if (!t || t.nodeType !== 1) return;
      // A bubbling event belongs to the nearest bound ancestor; a
      // non-bubbling one only to the element it was dispatched on,
      // exactly as addEventListener on that element would behave. The
      // exact match is also what stops mouseenter firing an ancestor's
      // binding for every descendant entered.
      var target = e.bubbles ? t.closest(selector) : (t.matches(selector) ? t : null);
      if (!target) return;

      // Outside / Window / Document bindings are served by the scoped
      // listeners in bindScopedEvent, not by root delegation. Skipping
      // them here keeps each binding firing exactly once.
      if (target.hasAttribute("data-tether-outside") || target.hasAttribute("data-tether-at")) return;

      handleBoundEvent(target, domEvent, dataAttr, e);
    });
  }

  // bindingKey identifies one binding on one element, so the timing
  // controls hold a window per element rather than per action. Two rows
  // in a list sharing an action own separate debounce, throttle and
  // frame slots - sharing one was invisible at a 300ms input debounce
  // and obvious at frame timing on pointer events.
  //
  // The id lives in a WeakMap rather than an attribute: the server
  // never renders it, so a morph would strip an attribute straight back
  // off, and the map lets the entry be collected with the element.
  // idiomorph preserves elements in place, so a morphed row keeps its
  // pending timers.
  var bindingIds = new WeakMap();
  var bindingSeq = 0;
  function bindingKey(el, dataAttr, action) {
    var id = bindingIds.get(el);
    if (!id) {
      id = String(++bindingSeq);
      bindingIds.set(el, id);
    }
    return id + ":" + dataAttr + ":" + action;
  }

  // handleBoundEvent runs the shared pipeline for a server event binding:
  // action resolution and prefixing, the once/confirm/stop guards,
  // optimistic signals, data collection, and the timing controls
  // (debounce, throttle, delay) before the event is sent. Both root
  // delegation (bindEventType) and the scoped Outside/Window/Document
  // listeners (bindScopedEvents) funnel through here so a binding behaves
  // identically wherever it listens.
  function handleBoundEvent(target, domEvent, dataAttr, e) {
    var action = target.getAttribute("data-" + dataAttr);
    if (!action) return;

    // FilterKey (bind.FilterKey): a keydown binding restricted to one key
    // ignores every other key before any side effect runs - so a wrong key
    // never spends the Once budget, fires an optimistic signal, or calls
    // preventDefault. Unrelated to data-fluent-key, the diff engine's
    // element identity.
    if (domEvent === "keydown") {
      var filterKey = target.getAttribute("data-tether-filterkey");
      if (filterKey && filterKey !== e.key) return;
    }

    // Once (bind.Once) fires the binding a single time. The guard is
    // here; the WeakSet is only updated once the binding commits below,
    // so a cancelled confirm dialog does not spend the single firing. A
    // morph that replaces the element yields a new node, which fires
    // once again.
    if (target.hasAttribute("data-tether-once") && firedOnce.has(target)) return;

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

    // The binding is now committed. Spend the Once budget (after any
    // cancelled confirm) and stop propagation (bind.Stop) if requested
    // so ancestor handlers do not also fire.
    if (target.hasAttribute("data-tether-once")) firedOnce.add(target);
    if (target.hasAttribute("data-tether-stop")) e.stopPropagation();

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
        // FilterKey was already applied at the top of handleBoundEvent.
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

    // Debounce input events. The leading-edge variant (bind.DebounceLeading)
    // sends on the first keystroke and then suppresses events until the
    // input has been quiet for the interval; the default trailing-edge
    // variant (bind.Debounce) waits for the pause before sending.
    if (domEvent === "input") {
      var timerKey = bindingKey(target, dataAttr, action);
      var leading = parseInt(target.getAttribute("data-tether-debounce-leading"));
      if (leading > 0) {
        var idle = !debounceTimers[timerKey];
        clearTimeout(debounceTimers[timerKey]);
        debounceTimers[timerKey] = setTimeout(function () {
          delete debounceTimers[timerKey];
        }, leading);
        if (idle) sendEvent(domEvent, action, data);
        return;
      }
      var delay = parseInt(target.getAttribute("data-tether-debounce")) || defaultDebounce;
      clearTimeout(debounceTimers[timerKey]);
      debounceTimers[timerKey] = setTimeout(function () {
        sendEvent(domEvent, action, data);
      }, delay);
      return;
    }

    // Throttle if configured
    var throttle = parseInt(target.getAttribute("data-tether-throttle"));
    if (throttle > 0) {
      var throttleKey = "throttle:" + bindingKey(target, dataAttr, action);
      if (debounceTimers[throttleKey]) return;
      debounceTimers[throttleKey] = setTimeout(function () {
        delete debounceTimers[throttleKey];
      }, throttle);
    }

    // Continuous events (mousemove, scroll, wheel, ...) fire far faster
    // than a round trip, so with no explicit timing they coalesce to one
    // event per animation frame. The pending sample is overwritten
    // rather than queued, so the server receives where the pointer
    // ENDED, not where it was when the frame opened - which is what a
    // leading-edge throttle would have sent. Everything above this point
    // (PreventDefault, Stop, optimistic signals, data collection) has
    // already run for every occurrence.
    if (continuousEvents[domEvent] && !throttle &&
        !target.hasAttribute("data-tether-delay") &&
        !target.hasAttribute("data-tether-debounce")) {
      var frameKey = "frame:" + bindingKey(target, dataAttr, action);
      var pending = !!framePending[frameKey];
      framePending[frameKey] = { target: target, data: data };
      if (pending) return;
      requestAnimationFrame(function () {
        var latest = framePending[frameKey];
        delete framePending[frameKey];
        if (!latest) return;
        var eid = sendEvent(domEvent, action, latest.data);
        if (eid) disablePending(latest.target, eid);
      });
      return;
    }

    // fire sends the event and starts the element's pending/disable
    // state. bind.Delay defers it by a fixed interval; without a delay
    // it runs synchronously.
    function fire() {
      var eid = sendEvent(domEvent, action, data);
      if (eid) disablePending(target, eid);
    }

    var delayMs = parseInt(target.getAttribute("data-tether-delay"));
    if (delayMs > 0) {
      setTimeout(fire, delayMs);
    } else {
      fire();
    }

    // Reset form fields after submit only when explicitly requested.
    // In a server-driven framework the server controls field values
    // via the re-render - auto-resetting races the server's state.
    if (domEvent === "submit" && target.hasAttribute("data-tether-reset")) {
      target.reset();
    }
  }

  // bindScopedEvents wires the Outside / Window / Document modifiers.
  // These bindings cannot use root delegation: an Outside click lands on
  // a different element than the one that owns the binding, and a
  // Window/Document binding must catch events that never enter the tether
  // root. One delegated listener per event type is attached at both the
  // document and window level; it queries the DOM live at dispatch time,
  // so morphed-in bindings are picked up without any rebinding.
  // bindScopedEvent attaches the document- and window-level listeners a
  // bind.Outside / bind.Window / bind.Document binding needs. Attached
  // only for the types a page actually scopes: a scoped handler queries
  // the whole document on every dispatch, so binding them for every
  // discovered type would make a wheel or mousemove binding pay two
  // full-document queries per tick. resize only ever fires on window, so
  // without this path bind.On("resize", ...) would never reach the
  // server at all.
  function bindScopedEvent(domEvent) {
    if (scopedTypes[domEvent]) return;
    scopedTypes[domEvent] = true;
    var dataAttr = "tether-event-" + domEvent;
    listen(document, domEvent, makeScopedHandler(domEvent, dataAttr, "document"));
    listen(window, domEvent, makeScopedHandler(domEvent, dataAttr, "window"));
  }

  // makeScopedHandler returns a delegated listener for one event type at
  // one scope ("document" or "window"). Outside detection runs only on
  // the document-scope listener so it fires exactly once per event; a
  // window/document binding fires from the listener whose scope matches
  // its data-tether-at value, again exactly once.
  function makeScopedHandler(domEvent, dataAttr, scope) {
    return function (e) {
      var els = safeQueryAll(document, "[data-" + dataAttr + "]");
      for (var i = 0; i < els.length; i++) {
        var el = els[i];
        if (!el.getAttribute("data-" + dataAttr)) continue;

        // Outside takes priority: fire when the event happened outside
        // the element. Guarded to the document scope to avoid a double
        // fire from the window-level listener.
        if (el.hasAttribute("data-tether-outside")) {
          if (scope === "document" && !el.contains(e.target)) {
            handleBoundEvent(el, domEvent, dataAttr, e);
          }
          continue;
        }

        // Window/Document scope: fire regardless of where the event
        // occurred, from the matching listener only.
        if (el.getAttribute("data-tether-at") === scope) {
          handleBoundEvent(el, domEvent, dataAttr, e);
        }
      }
    };
  }

  // Events raised while the WebSocket is still connecting (e.g. a
  // viewport trigger on initial load) are queued and flushed on open
  // instead of being dropped.
  var sendQueue = [];
  var maxSendQueue = 100;

  function flushSendQueue() {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    var queued = sendQueue;
    sendQueue = [];
    for (var i = 0; i < queued.length; i++) {
      ws.send(queued[i]);
    }
  }

  // fetchSeq orders stateless-mode responses. Responses can arrive
  // out of order (two rapid clicks); only the newest request's
  // response is applied so an older render can't overwrite a newer one.
  var fetchSeq = 0;

  // parseHTMLUpdate converts an HTML wire-format response into the
  // standard update message shape so applyMessage handles both
  // formats identically. The body is morph fragments, optionally
  // followed by a <template data-tether-effects> JSON island. When
  // keyed is true each top-level element is a targeted fragment
  // addressed by its data-fluent-key; otherwise the whole body is a
  // root morph.
  function parseHTMLUpdate(text, keyed, eventID) {
    var msg = { type: "update", event_id: eventID };

    var template = document.createElement("template");
    template.innerHTML = text;

    var fxEl = template.content.querySelector("template[data-tether-effects]");
    if (fxEl) {
      var raw = fxEl.content ? fxEl.content.textContent : fxEl.textContent;
      try {
        var fx = JSON.parse(raw);
        for (var k in fx) msg[k] = fx[k];
      } catch (err) {
        reportError("parse", "failed to parse effects island: " + err, false, "effects-island-parse", fxEl);
      }
      fxEl.parentNode.removeChild(fxEl);
    }

    msg.morphs = [];
    if (keyed) {
      var children = template.content.children;
      for (var i = 0; i < children.length; i++) {
        var key = children[i].getAttribute("data-fluent-key");
        if (key) msg.morphs.push({ key: key, html: children[i].outerHTML });
      }
    } else {
      // Re-serialise the remaining content (the island is gone) so
      // applyMorph's innerHTML-mode root morph gets a plain string.
      var holder = document.createElement("div");
      holder.appendChild(template.content);
      if (holder.innerHTML) {
        msg.morphs.push({ key: "", html: holder.innerHTML });
      }
    }
    return msg;
  }

  function sendEvent(type, action, data) {
    if (devMode) {
      console.log("tether: event", {type: type, action: action, data: data});
    }
    var id = String(++eventCounter);
    var envelope = {type: type, action: action, data: data, event_id: id};
    // Stateless auto-fragments: echo the current hash map so the
    // server can send back only the fragments that changed.
    if (connectionMode === "fetch" && fragmentHashes) {
      envelope.hashes = fragmentHashes;
    }
    var payload = JSON.stringify(envelope);

    if (connectionMode === "fetch") {
      var url = location.protocol + "//" + location.host + endpoint;
      var seq = ++fetchSeq;
      fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: payload
      }).then(function (resp) {
        if (!resp.ok) { restorePending(id); return null; }
        // The response format is sniffed from Content-Type: JSON is
        // the default envelope; text/html is the HTML wire format
        // (fragments as the body, effects in a JSON island).
        var ct = resp.headers.get("Content-Type") || "";
        if (ct.indexOf("text/html") === 0) {
          var keyed = resp.headers.get("Tether-Morph") === "keyed";
          return resp.text().then(function (text) {
            return parseHTMLUpdate(text, keyed, id);
          });
        }
        return resp.json();
      }).then(function (msg) {
        if (!msg) return;
        if (seq !== fetchSeq) { restorePending(id); return; }
        applyMessage(msg);
      }).catch(function (err) {
        reportError("fetch", "page event failed: " + err, false, "page-event-failed");
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
        reportError("fetch", "event POST failed: " + err, true, "event-post-failed");
        restorePending(id);
        if (backgroundSync) queueFailedEvent(payload);
      });
      return id;
    }

    if (!ws || ws.readyState !== WebSocket.OPEN) {
      if (ws && ws.readyState === WebSocket.CONNECTING && sendQueue.length < maxSendQueue) {
        sendQueue.push(payload);
        return id;
      }
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

  // Boundness lives in a WeakSet keyed by the DOM node, not in a DOM
  // attribute. Morphs sync attributes from server HTML - which never
  // contains a bound marker - so an attribute would be stripped on
  // every morph while the reused node kept its listener, and the
  // post-morph rebind would then stack a second listener (then a
  // third...), sending the action once per morph.
  var boundEditables = new WeakSet();

  function setupEditable(el) {
    if (boundEditables.has(el)) return;
    boundEditables.add(el);
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

    var candidates = container.querySelectorAll('button, [href], input, select, textarea, [role="button"], [role="link"], [tabindex]:not([tabindex="-1"])');
    // Disabled and hidden elements are not tab stops - including them
    // as trap boundaries lets Tab escape the container.
    var focusables = Array.prototype.filter.call(candidates, function (el) {
      return !el.disabled && el.offsetParent !== null;
    });
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

  // --- Client-side signal actions ---
  //
  // Signal actions let developers toggle or set signal values without
  // a server round-trip. All signal bindings (bind.Show, bind.Class,
  // bind.Text, etc.) react instantly. The server can override any
  // client-set signal via Session.Signal at any time.

  function handleSignalActions(e) {
    var toggle = e.target.closest("[data-tether-toggle-signal]");
    if (toggle) {
      var key = toggle.getAttribute("data-tether-toggle-signal");
      setSignal(key, !isTruthy(Tether.signals[key]));
      return;
    }

    var setter = e.target.closest("[data-tether-set-signal]");
    if (setter) {
      var raw = setter.getAttribute("data-tether-set-signal");
      var idx = raw.indexOf(" ");
      var key = idx === -1 ? raw : raw.substring(0, idx);
      var value = parseSignalValue(idx === -1 ? "" : raw.substring(idx + 1));
      setSignal(key, value);
    }
  }

  // --- Client-side events ---
  //
  // data-tether-emit="name selector" dispatches a client event to the
  // elements matching selector on click. Receivers carry
  // data-tether-on-<name>="verb args" (see bind.OnClientEvent) and run a
  // signal action in response. This keeps sibling coordination - clearing
  // a search box, closing another dropdown - entirely on the client.

  var CLIENT_EVENT = "tether:client";

  function handleEmit(e) {
    var trigger = e.target.closest("[data-tether-emit]");
    if (!trigger) return;

    var raw = trigger.getAttribute("data-tether-emit");
    var idx = raw.indexOf(" ");
    if (idx === -1) return;
    var name = raw.substring(0, idx);
    var selector = raw.substring(idx + 1);

    var targets = safeQueryAll(document, selector);
    for (var i = 0; i < targets.length; i++) {
      targets[i].dispatchEvent(new CustomEvent(CLIENT_EVENT, {
        detail: { name: name },
        bubbles: true
      }));
    }
  }

  function receiveClientEvent(e) {
    var el = e.target;
    if (!el || el.nodeType !== 1) return;
    var spec = el.getAttribute("data-tether-on-" + e.detail.name);
    if (!spec) return;

    var idx = spec.indexOf(" ");
    var verb = idx === -1 ? spec : spec.substring(0, idx);
    var args = idx === -1 ? "" : spec.substring(idx + 1);

    if (verb === "toggle-signal") {
      setSignal(args, !isTruthy(window.Tether.signals[args]));
      return;
    }
    if (verb === "set-signal") {
      var sp = args.indexOf(" ");
      var key = sp === -1 ? args : args.substring(0, sp);
      var value = parseSignalValue(sp === -1 ? "" : args.substring(sp + 1));
      setSignal(key, value);
    }

    // If the receiver is a filter input, its value may have just
    // changed (e.g. a Clear button set it empty). Re-run the filter so
    // the hidden items reappear without waiting for a keystroke.
    if (el.hasAttribute("data-tether-filter")) applyFilter(el);
  }

  // --- Client-side filtering ---
  //
  // data-tether-filter="selector" on a text input hides items in the
  // matched container whose text does not contain the query. Items are
  // the elements marked data-tether-filter-item, or the container's
  // direct children when none are marked. No server round-trip.

  function handleFilter(e) {
    var input = e.target.closest("[data-tether-filter]");
    if (input) applyFilter(input);
  }

  function applyFilter(input) {
    var container = safeQuery(document, input.getAttribute("data-tether-filter"));
    if (!container) return;

    var query = String(input.value || "").toLowerCase();
    var items = container.querySelectorAll("[data-tether-filter-item]");
    if (items.length === 0) items = container.children;

    for (var i = 0; i < items.length; i++) {
      var match = items[i].textContent.toLowerCase().indexOf(query) !== -1;
      items[i].style.display = match ? "" : "none";
    }
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
    { attr: "data-tether-hotkey", script: "tether-hotkey.js" },
    { attr: "data-tether-timer", script: "tether-timer.js" },
    { attr: "data-tether-selectable", script: "tether-select.js" },
    { attr: "data-tether-draggable", script: "tether-drag-and-drop.js" },
    { attr: "data-tether-sortable", script: "tether-drag-and-drop.js" },
    { attr: "data-tether-swipe", script: "tether-touch.js" },
    { attr: "data-tether-longpress", script: "tether-touch.js" },
    { attr: "data-tether-template", script: "tether-template.js" }
  ];
  var loadedExtensions = {};

  // runtimeVersion extracts the ?v= cache stamp from any server-
  // injected /_tether/ script tag. Empty when none is found.
  function runtimeVersion() {
    var s = document.querySelector('script[src*="/_tether/"]');
    if (s) {
      var m = (s.getAttribute("src") || "").match(/[?&]v=([^&]+)/);
      if (m) return m[1];
    }
    return "";
  }

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
      // Reuse the version stamp the server put on the initial script
      // tags so lazily loaded extensions hit the same HTTP cache
      // entry instead of busting it on every load with a timestamp.
      var v = runtimeVersion();
      tag.src = "/_tether/" + ext.script + (v ? "?v=" + v : "");
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
  window.Tether.setSignal = setSignal;

  // getSignal reads a signal value. Prefer this over touching
  // Tether.signals directly - the storage may change shape.
  window.Tether.getSignal = function (key) {
    return window.Tether.signals[key];
  };

  // isTruthy is the truthiness rule the core uses for bind.Show,
  // bind.Hide, bind.Class, and bind.Attr. Exposed so extensions treat
  // signal values identically.
  window.Tether.isTruthy = isTruthy;

  // findPrefix resolves the data-tether-prefix chain above an element
  // so extensions can namespace actions exactly like core events do.
  window.Tether.findPrefix = findPrefix;

  // trackClientClasses / trackClientAttrs register client-managed DOM
  // state so it survives server morphs (see preserveClientState).
  window.Tether.trackClientClasses = trackClientClasses;
  window.Tether.trackClientAttrs = trackClientAttrs;

  // Lifecycle subscriptions. Each returns an unsubscribe function.
  //
  //   Tether.onSignalChange(function (key, value) { ... })
  //   Tether.onUpdate(function (root) { ... })       // after each server update
  //   Tether.onElementAdded(function (el) { ... })   // morph added an element
  //   Tether.onElementRemoved(function (el) { ... }) // morph is removing one
  //
  // onElementAdded/Removed fire for the top-level node idiomorph
  // touched; scan el.querySelectorAll for descendants. Listeners are
  // guarded - a throwing callback is reported and skipped.
  window.Tether.onSignalChange = function (fn) { return addListener(signalListeners, fn); };
  window.Tether.onUpdate = function (fn) { return addListener(updateListeners, fn); };
  window.Tether.onElementAdded = function (fn) { return addListener(addedListeners, fn); };
  window.Tether.onElementRemoved = function (fn) { return addListener(removedListeners, fn); };
})();
