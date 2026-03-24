// tether-drag-and-drop.js - drag and drop extension for Tether.
//
// Loaded automatically when the initial page render contains any
// element with data-tether-draggable. Extension scripts are included
// once during the initial GET - if the first view does not render
// draggable elements (e.g. a login page), add a hidden marker element
// with the attribute so the script loads upfront.
//
// Handles the HTML5 Drag and Drop API: marks draggable elements,
// manages drop targets, provides visual feedback, and fires tether
// events on drop with merged data from source and target.

(function () {
  "use strict";

  var root = null;
  var devMode = false;
  var eventDataPrefix = "data-tether-data-";
  var dragClass = "tether-dragging";
  var overClass = "tether-drag-over";

  function log() {
    if (devMode) console.log.apply(console, ["tether-dnd:"].concat(Array.prototype.slice.call(arguments)));
  }

  function init() {
    root = document.querySelector("[data-tether-root]");
    if (!root) return;
    devMode = root.hasAttribute("data-tether-dev");
    log("init", "draggables:", root.querySelectorAll("[data-tether-draggable]").length,
        "targets:", root.querySelectorAll("[data-tether-drop-target]").length);
    bindDraggables(root);
    bindDropTargets(root);
  }

  // --- Draggable elements ---

  function bindDraggables(container) {
    var els = container.querySelectorAll("[data-tether-draggable]");
    for (var i = 0; i < els.length; i++) {
      setupDraggable(els[i]);
    }
  }

  function setupDraggable(el) {
    if (el.hasAttribute("data-tether-drag-bound")) return;
    el.setAttribute("data-tether-drag-bound", "");
    el.setAttribute("draggable", "true");

    el.addEventListener("dragstart", function (e) {
      el.classList.add(dragClass);

      // Collect all tether-data-* attributes and store them on the
      // drag transfer so the drop target can read them.
      var data = collectData(el);
      log("dragstart", data);
      e.dataTransfer.setData("application/tether", JSON.stringify(data));
      e.dataTransfer.effectAllowed = "move";
    });

    el.addEventListener("dragend", function () {
      el.classList.remove(dragClass);
      clearOverStates();
    });
  }

  // --- Drop targets ---

  function bindDropTargets(container) {
    var els = container.querySelectorAll("[data-tether-drop-target]");
    for (var i = 0; i < els.length; i++) {
      setupDropTarget(els[i]);
    }
  }

  function setupDropTarget(el) {
    if (el.hasAttribute("data-tether-drop-bound")) return;
    el.setAttribute("data-tether-drop-bound", "");

    el.addEventListener("dragover", function (e) {
      // Must preventDefault to allow drop.
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
    });

    el.addEventListener("dragenter", function (e) {
      e.preventDefault();
      el.classList.add(overClass);
    });

    el.addEventListener("dragleave", function (e) {
      // Only remove the class when leaving the target itself, not
      // when entering a child element.
      if (e.relatedTarget && el.contains(e.relatedTarget)) return;
      el.classList.remove(overClass);
    });

    el.addEventListener("drop", function (e) {
      e.preventDefault();
      el.classList.remove(overClass);

      var action = el.getAttribute("data-tether-drop-target");
      if (!action) return;

      // Merge data from the dragged element and the drop target.
      var sourceData = {};
      var raw = e.dataTransfer.getData("application/tether");
      log("drop raw dataTransfer:", raw);
      if (raw) {
        try { sourceData = JSON.parse(raw); } catch (_) {}
      }

      var targetData = collectData(el);
      var merged = {};

      // Source data first, then target data overlays. This lets the
      // drop target add context (e.g. which column was dropped on)
      // while preserving the dragged item's identity.
      var key;
      for (key in sourceData) {
        if (sourceData.hasOwnProperty(key)) merged[key] = sourceData[key];
      }
      for (key in targetData) {
        if (targetData.hasOwnProperty(key)) merged[key] = targetData[key];
      }

      log("drop merged:", merged, "action:", action);

      // Prefix support: if the drop target is inside a prefixed
      // container, prepend the prefix to the action.
      var prefix = findPrefix(el);
      if (prefix && action.indexOf(prefix + ".") !== 0) {
        action = prefix + "." + action;
      }

      Tether.sendEvent("drop", action, merged);
    });
  }

  // --- Helpers ---

  // collectData gathers all data-tether-data-* attributes from an element.
  function collectData(el) {
    var data = {};
    for (var i = 0; i < el.attributes.length; i++) {
      var attr = el.attributes[i];
      if (attr.name.indexOf(eventDataPrefix) === 0) {
        var key = attr.name.substring(eventDataPrefix.length);
        data[key] = attr.value;
      }
    }
    return data;
  }

  // findPrefix walks up from el to the tether root collecting
  // data-tether-prefix values. Matches the logic in tether.js.
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

  // clearOverStates removes the drag-over class from all targets.
  // Called on dragend to clean up any lingering visual state.
  function clearOverStates() {
    var overs = root ? root.querySelectorAll("." + overClass) : [];
    for (var i = 0; i < overs.length; i++) {
      overs[i].classList.remove(overClass);
    }
  }

  // Re-bind after server updates.
  document.addEventListener("tether:update", function (e) {
    var target = e.detail && e.detail.root ? e.detail.root : root;
    if (target) {
      var d = target.querySelectorAll("[data-tether-draggable]").length;
      var t = target.querySelectorAll("[data-tether-drop-target]").length;
      log("tether:update rebind", "draggables:", d, "targets:", t);
      bindDraggables(target);
      bindDropTargets(target);
    }
  });

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
