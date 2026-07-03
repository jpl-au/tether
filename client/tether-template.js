// tether-template.js - signal-driven client-side list rendering.
//
// Loaded automatically when any element uses data-tether-template (see
// bind.Template). A <template> element carries the markup for one item
// with {{field}} placeholders; when its signal - a JSON array pushed via
// Session.Signal - changes, this extension stamps one clone per element
// into the target container. Entirely client-side: the server sends the
// data once, the client re-renders locally with no round-trip.

(function () {
  "use strict";

  if (!window.Tether) return;

  // escapeHTML keeps interpolated values inert. Item data comes from the
  // server as a signal, but rendering it as markup on the client means
  // any string field could otherwise inject elements.
  function escapeHTML(value) {
    if (value == null) return "";
    return String(value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  // stamp fills one item's markup by replacing {{field}} placeholders.
  // {{.}} refers to the whole item, for arrays of scalars.
  function stamp(markup, item) {
    return markup.replace(/\{\{\s*([\w.]+)\s*\}\}/g, function (_, field) {
      var value = field === "." ? item : (item == null ? "" : item[field]);
      return escapeHTML(value);
    });
  }

  // parse pulls the "signal target" pair off a template element.
  function parse(el) {
    var raw = el.getAttribute("data-tether-template") || "";
    var idx = raw.indexOf(" ");
    if (idx === -1) return null;
    return { signal: raw.substring(0, idx), target: raw.substring(idx + 1) };
  }

  // render writes the current signal value into the template's target.
  // A missing signal renders nothing; a non-array (or JSON string that
  // parses to an array) is coerced so the server can push either shape.
  function render(el) {
    var spec = parse(el);
    if (!spec) return;

    var target = document.querySelector(spec.target);
    if (!target) return;

    var data = window.Tether.getSignal(spec.signal);
    if (typeof data === "string") {
      try { data = JSON.parse(data); } catch (e) { data = null; }
    }
    if (!Array.isArray(data)) {
      target.innerHTML = "";
      return;
    }

    var markup = el.innerHTML;
    var out = "";
    for (var i = 0; i < data.length; i++) {
      out += stamp(markup, data[i]);
    }
    target.innerHTML = out;
  }

  function renderAll() {
    var templates = document.querySelectorAll("[data-tether-template]");
    for (var i = 0; i < templates.length; i++) {
      render(templates[i]);
    }
  }

  // Re-render the templates bound to a signal when it changes, and
  // re-scan after each server update in case a morph added a template or
  // its target. The initial pass covers data present on first paint.
  window.Tether.onSignalChange(function (key) {
    var templates = document.querySelectorAll("[data-tether-template]");
    for (var i = 0; i < templates.length; i++) {
      var spec = parse(templates[i]);
      if (spec && spec.signal === key) render(templates[i]);
    }
  });

  window.Tether.onUpdate(renderAll);
  renderAll();
})();
