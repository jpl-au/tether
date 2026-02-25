// fluent-poly.js — client runtime for Fluent Poly reactive UI.
//
// This script is injected automatically by the poly handler. It connects
// to the server via WebSocket, applies patches to the DOM using idiomorph,
// and sends user events back to the server. The developer never imports
// or configures this file directly.
//
// On DOMContentLoaded it finds [data-poly-root], reads the endpoint from
// data-poly-endpoint, opens a WebSocket, binds event delegation, and
// starts applying patches.

// Poly.hooks is the public API for JS interop. Developers register
// named hooks with mounted/updated/destroyed callbacks:
//
//   Poly.hooks.chart = {
//     mounted: function(el) { /* init chart library */ },
//     updated: function(el) { /* refresh chart */ },
//     destroyed: function(el) { /* teardown */ }
//   };
window.Poly = window.Poly || {};
window.Poly.hooks = window.Poly.hooks || {};

(function () {
  "use strict";

  var root = null;
  var endpoint = "";
  var sessionID = "";
  var ws = null;
  var retryDelay = 0;
  var initialRetryDelay = 0;
  var maxRetryDelay = 0;
  var defaultDebounce = 0;
  var transitionTimeout = 0;
  var debounceTimers = {};
  var leavingNodes = new Set();
  var pendingElements = {};
  var eventCounter = 0;
  var transportMode = "ws"; // "ws", "sse", or "auto" — set from data-poly-transport
  var connectionMode = "ws";
  var eventSource = null;
  var wsOpened = false;
  var sseOpened = false;
  var devMode = false;
  var pendingCount = 0;

  // --- Initialisation ---

  document.addEventListener("DOMContentLoaded", function () {
    root = document.querySelector("[data-poly-root]");
    if (!root) return;

    endpoint = root.getAttribute("data-poly-endpoint") || "";
    sessionID = root.getAttribute("data-poly-session") || "";
    transportMode = root.getAttribute("data-poly-transport") || "ws";
    initialRetryDelay = parseInt(root.getAttribute("data-poly-retry-delay")) || 1000;
    retryDelay = initialRetryDelay;
    maxRetryDelay = parseInt(root.getAttribute("data-poly-max-retry-delay")) || 30000;
    defaultDebounce = parseInt(root.getAttribute("data-poly-debounce-default")) || 300;
    transitionTimeout = parseInt(root.getAttribute("data-poly-transition-timeout")) || 5000;
    devMode = root.hasAttribute("data-poly-dev");
    connectionMode = (transportMode === "sse") ? "sse" : "ws";
    connect();
    bindEvents();

    // Dev mode: unregister any existing service worker so cached assets
    // are never served stale during development.
    if (devMode && "serviceWorker" in navigator) {
      navigator.serviceWorker.getRegistrations().then(function (regs) {
        for (var i = 0; i < regs.length; i++) regs[i].unregister();
      });
    } else if (root.hasAttribute("data-poly-worker") && "serviceWorker" in navigator) {
      // Register service worker when enabled by the server. The worker
      // provides asset caching, offline page shells, push notification
      // handling, and background sync for SSE event resilience.
      navigator.serviceWorker.register("/_poly/poly-worker.js", { scope: "/" })
        .then(function (reg) {
          var pushKey = root.getAttribute("data-poly-push-key");
          if (pushKey && "PushManager" in window) {
            subscribePush(reg, pushKey);
          }
        })
        .catch(function (err) {
          console.warn("fluent-poly: service worker registration failed:", err);
        });
    }
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

  function connectWS() {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var url = protocol + "//" + location.host + endpoint;
    if (sessionID) url += "?session=" + sessionID;

    ws = new WebSocket(url);

    ws.onopen = function () {
      // On reconnect, sync the current URL with the server in case
      // the user navigated via back/forward while disconnected. The
      // browser's popstate fires even when offline, changing the URL
      // without notifying the server.
      var isReconnect = wsOpened;
      wsOpened = true;
      retryDelay = initialRetryDelay;
      if (root) root.classList.remove("poly-disconnected");
      hideReconnectBar();
      if (isReconnect) {
        sendNavigate(location.pathname + location.search);
      } else {
        mountExistingHooks();
      }
    };

    ws.onmessage = function (e) {
      var msg;
      try {
        msg = JSON.parse(e.data);
      } catch (_) {
        return;
      }
      applyMessage(msg);
    };

    ws.onclose = function () {
      if (root) root.classList.add("poly-disconnected");
      showReconnectBar();
      if (devMode) {
        setTimeout(function () { location.reload(); }, retryDelay);
        return;
      }
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

    eventSource = new EventSource(url);

    eventSource.onopen = function () {
      // On reconnect, sync the current URL with the server in case
      // the user navigated via back/forward while disconnected.
      var isReconnect = sseOpened;
      sseOpened = true;
      retryDelay = initialRetryDelay;
      if (root) root.classList.remove("poly-disconnected");
      hideReconnectBar();
      if (isReconnect) {
        replayQueuedEvents();
        sendNavigate(location.pathname + location.search);
      } else {
        mountExistingHooks();
      }
    };

    eventSource.onmessage = function (e) {
      var msg;
      try {
        msg = JSON.parse(e.data);
      } catch (_) {
        return;
      }
      applyMessage(msg);
    };

    eventSource.onerror = function () {
      if (root) root.classList.add("poly-disconnected");
      showReconnectBar();
      if (devMode) {
        eventSource.close();
        setTimeout(function () { location.reload(); }, retryDelay);
        return;
      }
      // EventSource reconnects automatically — no manual retry needed.
    };
  }

  function scheduleReconnect() {
    setTimeout(function () {
      retryDelay = Math.min(retryDelay * 2, maxRetryDelay);
      connect();
    }, retryDelay);
  }

  // --- Reconnecting indicator ---
  //
  // A fixed bar at the top of the viewport that slides in when the
  // transport disconnects and slides out on reconnect. Created lazily
  // on first disconnect so there is no DOM cost when the connection
  // stays healthy. Developers can override the appearance via the
  // .poly-reconnecting CSS class.

  var reconnectBar = null;

  function createReconnectBar() {
    var bar = document.createElement("div");
    bar.className = "poly-reconnecting";
    bar.setAttribute("role", "status");
    bar.setAttribute("aria-live", "polite");
    bar.textContent = "Reconnecting\u2026";
    bar.style.cssText = [
      "position:fixed",
      "top:0",
      "left:0",
      "right:0",
      "z-index:2147483647",
      "background:#ef4444",
      "color:#fff",
      "text-align:center",
      "padding:6px 12px",
      "font:14px/1.4 system-ui,sans-serif",
      "transform:translateY(-100%)",
      "transition:transform .3s ease",
      "pointer-events:none"
    ].join(";");
    document.body.appendChild(bar);
    return bar;
  }

  function showReconnectBar() {
    if (!reconnectBar) reconnectBar = createReconnectBar();
    reconnectBar.textContent = devMode ? "Reloading\u2026" : "Reconnecting\u2026";
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
  // When the server provides a VAPID public key via data-poly-push-key,
  // the client subscribes to push notifications through the service
  // worker's PushManager. The subscription is sent to the server so it
  // can deliver notifications later via the push subpackage.

  function subscribePush(reg, vapidKey) {
    reg.pushManager.getSubscription().then(function (sub) {
      if (sub) {
        // Already subscribed — send to server in case it restarted.
        sendPushSubscription(sub);
        return;
      }
      return reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapidKey)
      }).then(function (sub) {
        sendPushSubscription(sub);
      });
    }).catch(function (err) {
      console.warn("fluent-poly: push subscription failed:", err);
    });
  }

  function sendPushSubscription(sub) {
    var url = location.protocol + "//" + location.host + endpoint;
    fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Poly-Session": sessionID,
        "X-Poly-Push-Subscribe": "true"
      },
      body: JSON.stringify(sub.toJSON())
    }).catch(function (err) {
      console.warn("fluent-poly: push subscription POST failed:", err);
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

  var EVENT_DB_NAME = "poly-events";
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
          reg.sync.register("poly-event-sync");
        });
      }
    }).catch(function () {
      // IndexedDB unavailable — event is lost. The user will need
      // to repeat the action after reconnect.
    });
  }

  function replayQueuedEvents() {
    // When a service worker is active, delegate replay to it via
    // Background Sync. The worker replays events for all sessions and
    // is already listening for the sync event. Replaying from both the
    // main thread and the worker would cause duplicate POSTs.
    if (navigator.serviceWorker && navigator.serviceWorker.controller && "SyncManager" in window) {
      navigator.serviceWorker.ready.then(function (reg) {
        reg.sync.register("poly-event-sync");
      });
      return;
    }

    // No active worker — replay from the main thread as a fallback.
    openEventDB().then(function (db) {
      var tx = db.transaction(EVENT_STORE, "readonly");
      var store = tx.objectStore(EVENT_STORE);
      var allReq = store.getAll();
      var keysReq = store.getAllKeys();
      tx.oncomplete = function () {
        var events = allReq.result;
        var keys = keysReq.result;
        var url = location.protocol + "//" + location.host + endpoint;
        for (var i = 0; i < events.length; i++) {
          // Only replay events for the current session.
          if (events[i].sessionID !== sessionID) continue;
          replayAndDeleteEvent(db, keys[i], events[i].payload, url);
        }
      };
    }).catch(function () {
      // IndexedDB unavailable — nothing to replay.
    });
  }

  function replayAndDeleteEvent(db, key, payload, url) {
    fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Poly-Session": sessionID
      },
      body: payload
    }).then(function (resp) {
      if (resp.ok) {
        var tx = db.transaction(EVENT_STORE, "readwrite");
        tx.objectStore(EVENT_STORE).delete(key);
      }
    }).catch(function () {
      // Still failing — leave in IndexedDB for the next attempt.
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
      if (msg.url) {
        if (msg.replace) {
          history.replaceState({}, "", msg.url);
        } else {
          history.pushState({}, "", msg.url);
        }
      }
      if (msg.title) {
        document.title = msg.title;
      }
      if (msg.flash) {
        for (var selector in msg.flash) {
          var el = document.querySelector(selector);
          if (el) {
            el.textContent = msg.flash[selector];
            (function (target) {
              setTimeout(function () { target.textContent = ""; }, 5000);
            })(el);
          }
        }
      }

      // Set focus on the designated element after all DOM updates.
      // Uses data-poly-autofocus (not data-poly-focus, which is the
      // event binding attribute for the Focus helper).
      var focusEl = root.querySelector("[data-poly-autofocus]");
      if (focusEl) focusEl.focus();
    });
  }

  // --- JS hooks ---
  //
  // Elements with data-poly-hook="name" receive lifecycle callbacks
  // when they are added, morphed, or removed from the DOM. Hooks are
  // registered on the global Poly.hooks object.

  function callHook(el, lifecycle) {
    var name = el.getAttribute("data-poly-hook");
    if (!name) return;
    var hook = window.Poly.hooks[name];
    if (hook && typeof hook[lifecycle] === "function") {
      hook[lifecycle](el);
    }
  }

  // callHookDeep invokes a lifecycle callback on the element itself and
  // on any descendant hook elements. Idiomorph only fires afterNodeAdded
  // for the top-level node — descendants are already part of its innerHTML
  // and need to be scanned separately.
  function callHookDeep(el, lifecycle) {
    callHook(el, lifecycle);
    var hookEls = el.querySelectorAll("[data-poly-hook]");
    for (var i = 0; i < hookEls.length; i++) {
      callHook(hookEls[i], lifecycle);
    }
  }

  // mountExistingHooks scans the DOM for hook elements that were rendered
  // in the initial HTML (before any morph). Called once on first connect
  // so hooks fire even when the page loads directly onto a hooked view.
  function mountExistingHooks() {
    if (!root) return;
    var hookEls = root.querySelectorAll("[data-poly-hook]");
    for (var i = 0; i < hookEls.length; i++) {
      callHook(hookEls[i], "mounted");
    }
  }

  // --- Loading / pending states ---
  //
  // Elements with data-poly-disable are disabled while an event is in
  // flight. The attribute value, if non-empty, replaces the element's
  // text content during the wait. All pending elements are restored
  // when the next server update arrives.

  function disablePending(el, eventID) {
    var entry = { el: el, disabled: el.hasAttribute("disabled") };

    if (el.hasAttribute("data-poly-disable")) {
      entry.text = el.textContent;
      var newText = el.getAttribute("data-poly-disable");
      el.setAttribute("disabled", "");
      if (newText) el.textContent = newText;
    }

    var indicatorSelector = el.getAttribute("data-poly-indicator");
    if (indicatorSelector) {
      entry.indicator = document.querySelector(indicatorSelector);
      if (entry.indicator) entry.indicator.classList.add("poly-pending");
    }

    if (++pendingCount === 1 && root) {
      root.classList.add("poly-loading");
    }

    pendingElements[eventID] = entry;
  }

  function restorePending(eventID) {
    if (!eventID) return;
    var entry = pendingElements[eventID];
    if (!entry) return;
    if (!entry.disabled) entry.el.removeAttribute("disabled");
    if (entry.text !== undefined) entry.el.textContent = entry.text;
    if (entry.indicator) entry.indicator.classList.remove("poly-pending");

    if (--pendingCount === 0 && root) {
      root.classList.remove("poly-loading");
    }

    delete pendingElements[eventID];
  }

  // --- Client state preservation ---
  //
  // Client-side toggles (data-poly-toggle-class, data-poly-toggle-attr)
  // modify the DOM without the server knowing. When a server morph arrives,
  // the new HTML won't contain the toggled classes or attributes. The
  // beforeNodeMorphed hook copies client-managed state onto the incoming
  // node so Idiomorph merges it into the live DOM.

  function preserveClientState(oldNode, newNode) {
    var trackedClasses = oldNode.getAttribute("data-poly-client-classes");
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
      newNode.setAttribute("data-poly-client-classes", trackedClasses);
    }

    var trackedAttrs = oldNode.getAttribute("data-poly-client-attrs");
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
      newNode.setAttribute("data-poly-client-attrs", trackedAttrs);
    }
  }

  var morphCallbacks = {
    beforeNodeAdded: function (newNode) {
      if (newNode.nodeType !== 1) return true;
      var name = newNode.getAttribute("data-poly-transition");
      if (name) {
        newNode.classList.add("poly-" + name + "-enter");
      }
      return true;
    },

    afterNodeAdded: function (newNode) {
      if (newNode.nodeType !== 1) return;
      callHookDeep(newNode, "mounted");
      var name = newNode.getAttribute("data-poly-transition");
      if (!name) return;
      // Force reflow so the browser registers the enter class,
      // then remove it to trigger the CSS transition.
      newNode.offsetHeight;
      newNode.classList.remove("poly-" + name + "-enter");
    },

    beforeNodeMorphed: function (oldNode, newNode) {
      if (oldNode.nodeType !== 1) return true;

      // Cancel any pending leave transition — the element is being
      // morphed back in rather than removed.
      if (leavingNodes.has(oldNode)) {
        leavingNodes.delete(oldNode);
        var name = oldNode.getAttribute("data-poly-transition");
        if (name) {
          oldNode.classList.remove("poly-" + name + "-leave");
        }
      }

      preserveClientState(oldNode, newNode);
      return true;
    },

    afterNodeMorphed: function (oldNode) {
      if (oldNode.nodeType !== 1) return;
      callHook(oldNode, "updated");
    },

    beforeNodeRemoved: function (oldNode) {
      if (oldNode.nodeType !== 1) return true;
      callHookDeep(oldNode, "destroyed");
      var name = oldNode.getAttribute("data-poly-transition");
      if (!name) return true;

      // Already leaving — let it finish
      if (leavingNodes.has(oldNode)) return false;

      leavingNodes.add(oldNode);
      oldNode.classList.add("poly-" + name + "-leave");

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

  // --- Patching and morphing ---

  function applyPatch(patch) {
    var el = document.querySelector('[data-poly-key="' + patch.key + '"]');
    if (!el) return;

    if (devMode) {
      console.log("fluent-poly: patch", patch.key);
      flashElement(el);
    }

    var template = document.createElement("template");
    template.innerHTML = patch.html;
    if (template.content.childElementCount > 1) {
      console.warn("fluent-poly: patch for key '" + patch.key + "' contains multiple root elements. Only the first will be used. Wrap them in a single container.");
    }
    var newEl = template.content.firstElementChild;
    if (!newEl) return;

    Idiomorph.morph(el, newEl, {callbacks: morphCallbacks});
  }

  function applyMorph(morph) {
    if (devMode) {
      console.log("fluent-poly: morph", morph.key || "root");
    }

    var template = document.createElement("template");
    template.innerHTML = morph.html;
    if (template.content.childElementCount > 1) {
      console.warn("fluent-poly: morph for key '" + morph.key + "' contains multiple root elements. Only the first will be used. Wrap them in a single container.");
    }
    var newEl = template.content.firstElementChild;
    if (!newEl) return;

    if (!morph.key) {
      // Empty key targets the root element. Use innerHTML mode so
      // idiomorph morphs root's children without replacing root itself
      // (which carries data-poly-root, data-poly-session, etc.).
      if (root) {
        if (devMode) flashElement(root);
        Idiomorph.morph(root, newEl, {morphStyle: "innerHTML", callbacks: morphCallbacks});
      }
    } else {
      // Scoped morph targets a keyed container.
      var el = document.querySelector('[data-poly-key="' + morph.key + '"]');
      if (el) {
        if (devMode) flashElement(el);
        Idiomorph.morph(el, newEl, {callbacks: morphCallbacks});
      }
    }
  }

  function flashElement(el) {
    var oldTransition = el.style.transition;
    var oldOutline = el.style.outline;
    el.style.transition = "none";
    el.style.outline = "2px solid rgba(59, 130, 246, 0.5)";
    el.style.outlineOffset = "-2px";
    requestAnimationFrame(function () {
      if (!el.isConnected) return;
      el.style.transition = "outline 0.5s ease-out";
      el.style.outline = "2px solid transparent";
      setTimeout(function () {
        if (!el.isConnected) return;
        el.style.transition = oldTransition;
        el.style.outline = oldOutline;
      }, 500);
    });
  }

  // --- Event delegation ---

  var eventTypes = [
    ["click", "poly-click"],
    ["input", "poly-input"],
    ["change", "poly-change"],
    ["submit", "poly-submit"],
    ["keydown", "poly-keydown"],
    ["focus", "poly-focus"],
    ["blur", "poly-blur"]
  ];

  function bindEvents() {
    for (var i = 0; i < eventTypes.length; i++) {
      bindEventType(eventTypes[i][0], eventTypes[i][1]);
    }
    root.addEventListener("click", handleToggles);
    root.addEventListener("click", handleLinks);
    window.addEventListener("keydown", handleFocusTrap);

    window.addEventListener("popstate", function () {
      sendNavigate(location.pathname + location.search);
    });
  }

  // --- Client-side navigation ---
  //
  // Anchors with data-poly-link are intercepted. Instead of a full page
  // load the JS pushes the URL into the browser history and sends a
  // navigate event to the server so HandleParams can update state.

  function handleLinks(e) {
    var link = e.target.closest("a[data-poly-link]");
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

  function bindEventType(domEvent, dataAttr) {
    root.addEventListener(domEvent, function (e) {
      var target = e.target.closest("[data-" + dataAttr + "]");
      if (!target) return;

      var action = target.getAttribute("data-" + dataAttr);
      if (!action) return;

      // Show a confirmation dialog if the element requests one.
      var confirmMsg = target.getAttribute("data-poly-confirm");
      if (confirmMsg && !window.confirm(confirmMsg)) return;

      // Prevent default for submit events and reset the form after
      // sending so the input fields clear. The server re-renders with
      // empty values but the form isn't inside a Dynamic key, so the
      // client needs to clear it locally.
      if (domEvent === "submit") {
        e.preventDefault();
      }

      var data = {};

      // Collect event-specific data
      if (domEvent === "input" || domEvent === "change") {
        data.value = target.value || "";
        // Checkboxes and radios always report their value attribute
        // (default "on"), which doesn't tell the server whether the
        // control is checked or unchecked. Send the checked state so
        // the server can distinguish between the two.
        if (target.type === "checkbox" || target.type === "radio") {
          data.checked = target.checked ? "true" : "false";
        }
      } else if (domEvent === "keydown") {
        // If data-poly-key is set, only send the event if it matches.
        var filter = target.getAttribute("data-poly-key");
        if (filter && filter !== e.key) return;

        data.key = e.key || "";
        if (e.ctrlKey) data.ctrl = "true";
        if (e.shiftKey) data.shift = "true";
        if (e.altKey) data.alt = "true";
        if (e.metaKey) data.meta = "true";
      } else if (domEvent === "submit") {
        var formData = new FormData(target);
        formData.forEach(function (value, key) {
          if (data[key]) {
            data[key] += "," + value;
          } else {
            data[key] = value;
          }
        });
      }

      // Debounce input events
      if (domEvent === "input") {
        var delay = parseInt(target.getAttribute("data-poly-debounce")) || defaultDebounce;
        var timerKey = dataAttr + ":" + action;

        clearTimeout(debounceTimers[timerKey]);
        debounceTimers[timerKey] = setTimeout(function () {
          sendEvent(domEvent, action, data);
        }, delay);
        return;
      }

      // Throttle if configured
      var throttle = parseInt(target.getAttribute("data-poly-throttle"));
      if (throttle > 0) {
        var throttleKey = "throttle:" + dataAttr + ":" + action;
        if (debounceTimers[throttleKey]) return;
        debounceTimers[throttleKey] = setTimeout(function () {
          delete debounceTimers[throttleKey];
        }, throttle);
      }

      var eid = sendEvent(domEvent, action, data);
      if (eid) disablePending(target, eid);

      // Clear form fields after submit unless the form opts out via
      // data-poly-preserve (used when the server controls field values
      // through a Dynamic key).
      if (domEvent === "submit" && !target.hasAttribute("data-poly-preserve")) {
        target.reset();
      }
    }, domEvent === "focus" || domEvent === "blur");
  }

  function sendEvent(type, action, data) {
    if (devMode) {
      console.log("fluent-poly: event", {type: type, action: action, data: data});
    }
    var id = String(++eventCounter);
    var payload = JSON.stringify({type: type, action: action, data: data, event_id: id});

    if (connectionMode === "sse") {
      var url = location.protocol + "//" + location.host + endpoint;
      fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Poly-Session": sessionID
        },
        body: payload
      }).then(function (resp) {
        // Restore loading state on non-2xx responses so the button
        // does not stay permanently disabled.
        if (!resp.ok) restorePending(id);
      }).catch(function () {
        restorePending(id);
        queueFailedEvent(payload);
      });
      return id;
    }

    if (!ws || ws.readyState !== WebSocket.OPEN) return null;
    ws.send(payload);
    return id;
  }

  // --- Client-side toggles ---
  //
  // Toggle directives run entirely in the browser. The server never
  // learns about them. When a server morph arrives, the Idiomorph
  // beforeNodeMorphed hook (see morphCallbacks above) copies the
  // client-managed state onto the incoming node so it survives.

  function handleToggles(e) {
    var trigger = e.target.closest("[data-poly-toggle-class], [data-poly-toggle-attr]");
    if (!trigger) return;

    var targetSelector = trigger.getAttribute("data-poly-toggle-target");
    var target = targetSelector ? document.querySelector(targetSelector) : trigger;
    if (!target) return;

    var toggleClass = trigger.getAttribute("data-poly-toggle-class");
    if (toggleClass) {
      var classes = toggleClass.split(/\s+/);
      for (var i = 0; i < classes.length; i++) {
        if (classes[i]) target.classList.toggle(classes[i]);
      }
      trackClientClasses(target, classes);
    }

    var toggleAttr = trigger.getAttribute("data-poly-toggle-attr");
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
    var tracked = el.getAttribute("data-poly-client-classes") || "";
    var set = tracked ? tracked.split(/\s+/) : [];
    for (var i = 0; i < classNames.length; i++) {
      if (classNames[i] && set.indexOf(classNames[i]) === -1) {
        set.push(classNames[i]);
      }
    }
    el.setAttribute("data-poly-client-classes", set.join(" "));
  }

  function handleFocusTrap(e) {
    if (e.key !== "Tab") return;

    var container = e.target.closest("[data-poly-focus-trap]");
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
    var tracked = el.getAttribute("data-poly-client-attrs") || "";
    var set = tracked ? tracked.split(/\s+/) : [];
    if (attrName && set.indexOf(attrName) === -1) {
      set.push(attrName);
    }
    el.setAttribute("data-poly-client-attrs", set.join(" "));
  }
})();
