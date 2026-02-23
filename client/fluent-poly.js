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

(function () {
  "use strict";

  var root = null;
  var endpoint = "";
  var sessionID = "";
  var ws = null;
  var retryDelay = 1000;
  var maxRetryDelay = 30000;
  var debounceTimers = {};

  // --- Initialisation ---

  document.addEventListener("DOMContentLoaded", function () {
    root = document.querySelector("[data-poly-root]");
    if (!root) return;

    endpoint = root.getAttribute("data-poly-endpoint") || "";
    sessionID = root.getAttribute("data-poly-session") || "";
    connect();
    bindEvents();
  });

  // --- WebSocket connection ---

  function connect() {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var url = protocol + "//" + location.host + endpoint;
    if (sessionID) url += "?session=" + sessionID;

    ws = new WebSocket(url);

    ws.onopen = function () {
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
      scheduleReconnect();
    };

    ws.onerror = function () {
      ws.close();
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
  }

  function applyPatch(patch) {
    var el = document.querySelector('[data-poly-key="' + patch.key + '"]');
    if (!el) return;

    var template = document.createElement("template");
    template.innerHTML = patch.html;
    var newEl = template.content.firstElementChild;
    if (!newEl) return;

    Idiomorph.morph(el, newEl);
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
      if (root) Idiomorph.morph(root, newEl, {morphStyle: "innerHTML"});
    } else {
      // Scoped morph targets a keyed container.
      var el = document.querySelector('[data-poly-key="' + morph.key + '"]');
      if (el) Idiomorph.morph(el, newEl);
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
  }

  function bindEventType(domEvent, dataAttr) {
    root.addEventListener(domEvent, function (e) {
      var target = e.target.closest("[data-" + dataAttr + "]");
      if (!target) return;

      var action = target.getAttribute("data-" + dataAttr);
      if (!action) return;

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
          debounceTimers[throttleKey] = null;
        }, throttle);
      }

      sendEvent(domEvent, action, data);

      // Clear form fields after submit so the user can type again
      // without manually selecting and deleting the old input.
      if (domEvent === "submit") {
        target.reset();
      }
    }, domEvent === "focus" || domEvent === "blur");
  }

  function sendEvent(type, action, data) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({
      type: type,
      action: action,
      data: data
    }));
  }
})();
