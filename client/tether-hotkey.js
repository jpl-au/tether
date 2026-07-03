// tether-hotkey.js - global keyboard shortcuts extension for Tether.
//
// Loaded automatically when any element uses data-tether-hotkey (see
// bind.Hotkey). The extension builds a registry (combo → {action,
// element}) on load and rebuilds it after each server update, so
// keydown lookups are O(1) with no CSS selector queries.
//
// Combo semantics:
//   - ctrl and meta (Cmd/Win) are distinct modifiers, so a ctrl combo
//     never swallows Cmd+C on macOS.
//   - "mod" matches the platform's primary command modifier - meta on
//     macOS, ctrl elsewhere - so one combo feels native everywhere.
//   - Hotkeys without ctrl/meta/alt do not fire while an editable
//     element has focus; typing "/" into a search box is text.

(function () {
  "use strict";

  var registry = {};
  var isMac = /Mac|iPhone|iPad|iPod/.test(navigator.platform || "");

  function build(root) {
    registry = {};
    if (!root) return;
    var els = root.querySelectorAll("[data-tether-hotkey]");
    for (var i = 0; i < els.length; i++) {
      var val = els[i].getAttribute("data-tether-hotkey");
      var spaceIdx = val.indexOf(" ");
      if (spaceIdx === -1) continue;
      var combo = val.substring(0, spaceIdx);
      var action = val.substring(spaceIdx + 1);
      registry[combo] = { action: action, el: els[i] };
    }
  }

  // isEditableTarget reports whether the element accepts typed text.
  function isEditableTarget(el) {
    if (!el || el.nodeType !== 1) return false;
    var tag = el.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
    return !!el.isContentEditable;
  }

  function onKeydown(e) {
    var key = e.key.toLowerCase();
    if (key === " ") key = "space";
    if (key === "control" || key === "shift" || key === "alt" || key === "meta") return;

    // Unmodified keys (and shift, which produces capitals and
    // punctuation) belong to the focused field. Ctrl/meta/alt combos
    // are commands and still fire from inputs.
    if (!e.ctrlKey && !e.metaKey && !e.altKey && isEditableTarget(e.target)) return;

    var mods = [];
    if (e.ctrlKey) mods.push("ctrl");
    if (e.metaKey) mods.push("meta");
    if (e.shiftKey) mods.push("shift");
    if (e.altKey) mods.push("alt");

    var combo = mods.concat([key]).join("-");
    var entry = registry[combo];
    if (!entry) {
      var primary = isMac ? "meta" : "ctrl";
      if (mods.indexOf(primary) !== -1) {
        var modCombo = mods.map(function (m) { return m === primary ? "mod" : m; }).concat([key]).join("-");
        entry = registry[modCombo];
        if (entry) combo = modCombo;
      }
    }
    if (!entry) return;

    e.preventDefault();

    var action = entry.action;
    var prefix = Tether.findPrefix(entry.el);
    if (prefix && action.indexOf(prefix + ".") !== 0) {
      action = prefix + "." + action;
    }

    Tether.sendEvent("hotkey", action, { combo: combo });
  }

  function init() {
    var root = document.querySelector("[data-tether-root]");
    if (!root) return;

    build(root);
    window.addEventListener("keydown", onKeydown);
    // Rebuild after each server update so hotkey elements added or
    // removed by morphs are reflected.
    Tether.onUpdate(build);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
