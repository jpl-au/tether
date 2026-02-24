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
  var retryDelay = 1000;
  var maxRetryDelay = 30000;
  var debounceTimers = {};
  var leavingNodes = new Set();
  var pendingElements = {};
  var eventCounter = 0;
  var transportMode = "ws"; // "ws", "sse", or "auto" — set from data-poly-transport
  var connectionMode = "ws";
  var eventSource = null;
  var wsOpened = false;

  // --- Initialisation ---

  document.addEventListener("DOMContentLoaded", function () {
    root = document.querySelector("[data-poly-root]");
    if (!root) return;

    endpoint = root.getAttribute("data-poly-endpoint") || "";
    sessionID = root.getAttribute("data-poly-session") || "";
    transportMode = root.getAttribute("data-poly-transport") || "ws";
    connectionMode = (transportMode === "sse") ? "sse" : "ws";
    connect();
    bindEvents();
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
      wsOpened = true;
      retryDelay = 1000;
      if (root) root.classList.remove("poly-disconnected");
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
      retryDelay = 1000;
      if (root) root.classList.remove("poly-disconnected");
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
      // EventSource reconnects automatically — no manual retry needed.
    };
  }

  function scheduleReconnect() {
    setTimeout(function () {
      retryDelay = Math.min(retryDelay * 2, maxRetryDelay);
      connect();
    }, retryDelay);
  }

  // --- Message handling ---

  function applyMessage(msg) {
    if (msg.type !== "update") return;

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

    // Set focus on the designated element after all DOM updates.
    var focusEl = root.querySelector("[data-poly-focus]");
    if (focusEl) focusEl.focus();
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

  // --- Loading / pending states ---
  //
  // Elements with data-poly-disable are disabled while an event is in
  // flight. The attribute value, if non-empty, replaces the element's
  // text content during the wait. All pending elements are restored
  // when the next server update arrives.

  function disablePending(el, eventID) {
    if (!el || !el.hasAttribute("data-poly-disable")) return;
    var text = el.getAttribute("data-poly-disable");
    pendingElements[eventID] = {
      el: el,
      text: text ? el.textContent : null,
      disabled: el.hasAttribute("disabled")
    };
    el.setAttribute("disabled", "");
    if (text) el.textContent = text;
  }

  function restorePending(eventID) {
    if (!eventID) return;
    var entry = pendingElements[eventID];
    if (!entry) return;
    if (!entry.disabled) entry.el.removeAttribute("disabled");
    if (entry.text !== null) entry.el.textContent = entry.text;
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
      callHook(newNode, "mounted");
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
      callHook(oldNode, "destroyed");
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

      oldNode.addEventListener("transitionend", function handler() {
        oldNode.removeEventListener("transitionend", handler);
        remove();
      });

      // Fallback: remove after 5s if transitionend never fires
      // (e.g. no CSS transition defined, or transition property removed)
      setTimeout(remove, 5000);

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

    var template = document.createElement("template");
    template.innerHTML = patch.html;
    var newEl = template.content.firstElementChild;
    if (!newEl) return;

    Idiomorph.morph(el, newEl, {callbacks: morphCallbacks});
  }

  function applyMorph(morph) {
    var template = document.createElement("template");
    template.innerHTML = morph.html;
    var newEl = template.content.firstElementChild;
    if (!newEl) return;

    if (!morph.key) {
      // Empty key targets the root element. Use innerHTML mode so
      // idiomorph morphs root's children without replacing root itself
      // (which carries data-poly-root, data-poly-session, etc.).
      if (root) Idiomorph.morph(root, newEl, {morphStyle: "innerHTML", callbacks: morphCallbacks});
    } else {
      // Scoped morph targets a keyed container.
      var el = document.querySelector('[data-poly-key="' + morph.key + '"]');
      if (el) Idiomorph.morph(el, newEl, {callbacks: morphCallbacks});
    }
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

    var href = link.getAttribute("href");
    if (!href || href.indexOf("://") !== -1 || href.indexOf("//") === 0) return;

    e.preventDefault();
    history.pushState({}, "", href);
    sendNavigate(href);
  }

  function sendNavigate(url) {
    var idx = url.indexOf("?");
    var path = idx === -1 ? url : url.substring(0, idx);
    var search = idx === -1 ? "" : url.substring(idx + 1);
    sendEvent("navigate", "", { path: path, search: search });
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
      } else if (domEvent === "keydown") {
        data.key = e.key || "";
        if (e.ctrlKey) data.ctrl = "true";
        if (e.shiftKey) data.shift = "true";
        if (e.altKey) data.alt = "true";
        if (e.metaKey) data.meta = "true";
      } else if (domEvent === "submit") {
        var formData = new FormData(target);
        formData.forEach(function (value, key) {
          data[key] = value;
        });
      }

      // Debounce input events
      if (domEvent === "input") {
        var delay = parseInt(target.getAttribute("data-poly-debounce")) || 300;
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
      disablePending(target, eid);

      // Clear form fields after submit unless the form opts out via
      // data-poly-preserve (used when the server controls field values
      // through a Dynamic key).
      if (domEvent === "submit" && !target.hasAttribute("data-poly-preserve")) {
        target.reset();
      }
    }, domEvent === "focus" || domEvent === "blur");
  }

  function sendEvent(type, action, data) {
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
      });
      return id;
    }

    if (!ws || ws.readyState !== WebSocket.OPEN) return id;
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

  function trackClientAttrs(el, attrName) {
    var tracked = el.getAttribute("data-poly-client-attrs") || "";
    var set = tracked ? tracked.split(/\s+/) : [];
    if (attrName && set.indexOf(attrName) === -1) {
      set.push(attrName);
    }
    el.setAttribute("data-poly-client-attrs", set.join(" "));
  }
})();
