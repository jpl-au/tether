// tether-select.js - multi-select extension for Tether.
//
// Loaded automatically when any element uses data-tether-selectable
// (see bind.Selectable). Containers enable click, ctrl+click, and
// shift+click selection on children that carry data-tether-data-id.
// Selection is purely client-side via the tether-selected CSS class,
// registered with the core so it survives server morphs. Use
// data-tether-collect-selected on an action button to gather the
// selected IDs (handled by the core event pipeline).

(function () {
  "use strict";

  var lastSelected = null;

  function onClick(e) {
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
        Tether.trackClientClasses(item, ["tether-selected"]);
      }
    } else if (e.ctrlKey || e.metaKey) {
      // Toggle this item.
      item.classList.toggle("tether-selected");
      Tether.trackClientClasses(item, ["tether-selected"]);
    } else {
      // Single select: deselect all, select this one.
      for (var i = 0; i < items.length; i++) {
        items[i].classList.remove("tether-selected");
      }
      item.classList.add("tether-selected");
      Tether.trackClientClasses(item, ["tether-selected"]);
    }

    lastSelected = item;
  }

  function init() {
    var root = document.querySelector("[data-tether-root]");
    if (!root) return;
    root.addEventListener("click", onClick);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
