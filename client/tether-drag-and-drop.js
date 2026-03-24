// tether-drag-and-drop.js - drag and drop extension for Tether.
//
// Loaded automatically when the initial page render contains any
// element with data-tether-draggable. Extension scripts are included
// once during the initial GET - if the first view does not render
// draggable elements (e.g. a login page), add a hidden marker element
// with the attribute so the script loads upfront.
//
// Uses event delegation on the tether root - consistent with the core
// tether.js architecture. No per-element listeners are attached, so
// DOM morphing cannot create ghost listeners.

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

    markDraggable(root);

    // All DnD events are delegated to the root. This avoids ghost
    // listeners when idiomorph reuses DOM nodes during morphs.

    root.addEventListener("dragstart", function (e) {
      var el = e.target.closest("[data-tether-draggable]");
      if (!el) return;
      el.classList.add(dragClass);
      var data = collectData(el);
      log("dragstart", data);
      e.dataTransfer.setData("application/tether", JSON.stringify(data));
      e.dataTransfer.effectAllowed = "move";
    });

    root.addEventListener("dragend", function (e) {
      var el = e.target.closest("[data-tether-draggable]");
      if (el) el.classList.remove(dragClass);
      clearOverStates();
    });

    root.addEventListener("dragover", function (e) {
      var el = findDropZone(e.target);
      if (!el) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
    });

    root.addEventListener("dragenter", function (e) {
      var el = findDropZone(e.target);
      if (!el) return;
      e.preventDefault();
      el.classList.add(overClass);
    });

    root.addEventListener("dragleave", function (e) {
      var el = findDropZone(e.target);
      if (!el) return;
      if (e.relatedTarget && el.contains(e.relatedTarget)) return;
      el.classList.remove(overClass);
    });

    root.addEventListener("drop", function (e) {
      var el = findDropZone(e.target);
      if (!el) return;
      e.preventDefault();
      el.classList.remove(overClass);

      var action = el.getAttribute("data-tether-drop-target") ||
                   el.getAttribute("data-tether-sortable");
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
      var key;
      for (key in sourceData) {
        if (sourceData.hasOwnProperty(key)) merged[key] = sourceData[key];
      }
      for (key in targetData) {
        if (targetData.hasOwnProperty(key)) merged[key] = targetData[key];
      }

      // For sortable containers, calculate the drop index based on
      // the cursor position relative to the draggable children.
      if (el.hasAttribute("data-tether-sortable")) {
        merged.index = String(dropIndex(el, e.clientY));
      }

      log("drop merged:", merged, "action:", action);

      var prefix = findPrefix(el);
      if (prefix && action.indexOf(prefix + ".") !== 0) {
        action = prefix + "." + action;
      }

      Tether.sendEvent("drop", action, merged);
    });

    log("init (delegated)", "draggables:", root.querySelectorAll("[data-tether-draggable]").length,
        "targets:", root.querySelectorAll("[data-tether-drop-target]").length);
  }

  // markDraggable sets draggable="true" on all elements with the
  // data-tether-draggable attribute. The HTML5 DnD API requires this
  // attribute for non-image, non-link elements. Called on init and
  // after each morph.
  function markDraggable(container) {
    var els = container.querySelectorAll("[data-tether-draggable]");
    for (var i = 0; i < els.length; i++) {
      els[i].setAttribute("draggable", "true");
    }
  }

  // findDropZone walks up from the event target to find the nearest
  // drop target or sortable container.
  function findDropZone(target) {
    return target.closest("[data-tether-drop-target], [data-tether-sortable]");
  }

  // dropIndex calculates where in a sortable container the cursor is.
  // Returns the index at which the dropped item should be inserted,
  // based on the vertical midpoint of each draggable child.
  function dropIndex(container, clientY) {
    var children = container.querySelectorAll("[data-tether-draggable]");
    for (var i = 0; i < children.length; i++) {
      var rect = children[i].getBoundingClientRect();
      var mid = rect.top + rect.height / 2;
      if (clientY < mid) return i;
    }
    return children.length;
  }

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
  function clearOverStates() {
    var overs = root ? root.querySelectorAll("." + overClass) : [];
    for (var i = 0; i < overs.length; i++) {
      overs[i].classList.remove(overClass);
    }
  }

  // After server morphs, re-mark new draggable elements. Event
  // listeners are delegated so no re-binding is needed.
  document.addEventListener("tether:update", function (e) {
    var target = e.detail && e.detail.root ? e.detail.root : root;
    if (target) {
      markDraggable(target);
      log("tether:update", "draggables:", target.querySelectorAll("[data-tether-draggable]").length);
    }
  });

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
