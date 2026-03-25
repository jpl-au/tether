// tether-touch.js - touch gesture extension for Tether.
//
// Loaded automatically when any element renders data-tether-swipe or
// data-tether-longpress. Handles swipe (direction detection) and
// long-press (sustained touch) gestures on touch devices. Uses event
// delegation on the tether root.

(function () {
  "use strict";

  var root = null;
  var devMode = false;

  // Swipe thresholds.
  var minDistance = 30;   // pixels
  var maxTime = 500;      // ms

  // Long-press threshold.
  var holdTime = 500;     // ms
  var moveThreshold = 10; // pixels - cancel if finger moves more

  // Touch state.
  var touchStartX = 0;
  var touchStartY = 0;
  var touchStartTime = 0;
  var longPressTimer = null;
  var longPressTarget = null;

  function log() {
    if (devMode) console.log.apply(console, ["tether-touch:"].concat(Array.prototype.slice.call(arguments)));
  }

  function init() {
    root = document.querySelector("[data-tether-root]");
    if (!root) return;
    devMode = root.hasAttribute("data-tether-dev");

    root.addEventListener("touchstart", onTouchStart, { passive: true });
    root.addEventListener("touchmove", onTouchMove, { passive: true });
    root.addEventListener("touchend", onTouchEnd);

    log("init");
  }

  function onTouchStart(e) {
    if (!e.touches.length) return;
    var touch = e.touches[0];
    touchStartX = touch.clientX;
    touchStartY = touch.clientY;
    touchStartTime = Date.now();

    // Start long-press timer if the target is inside a longpress element.
    cancelLongPress();
    var el = e.target.closest("[data-tether-longpress]");
    if (el) {
      longPressTarget = el;
      longPressTimer = setTimeout(function () {
        fireLongPress(el);
        longPressTarget = null;
      }, holdTime);
    }
  }

  function onTouchMove(e) {
    if (!longPressTimer) return;
    if (!e.touches.length) return;
    var touch = e.touches[0];
    var dx = touch.clientX - touchStartX;
    var dy = touch.clientY - touchStartY;
    if (Math.abs(dx) > moveThreshold || Math.abs(dy) > moveThreshold) {
      cancelLongPress();
    }
  }

  function onTouchEnd(e) {
    cancelLongPress();

    var touch = e.changedTouches[0];
    if (!touch) return;

    var dx = touch.clientX - touchStartX;
    var dy = touch.clientY - touchStartY;
    var elapsed = Date.now() - touchStartTime;

    if (elapsed > maxTime) return;

    var absDx = Math.abs(dx);
    var absDy = Math.abs(dy);

    if (absDx < minDistance && absDy < minDistance) return;

    var el = e.target.closest("[data-tether-swipe]");
    if (!el) return;

    var direction;
    if (absDx > absDy) {
      direction = dx > 0 ? "right" : "left";
    } else {
      direction = dy > 0 ? "down" : "up";
    }

    var action = el.getAttribute("data-tether-swipe");
    if (!action) return;

    var prefix = findPrefix(el);
    if (prefix && action.indexOf(prefix + ".") !== 0) {
      action = prefix + "." + action;
    }

    log("swipe", direction, action);
    Tether.sendEvent("swipe", action, { direction: direction });
  }

  function fireLongPress(el) {
    var action = el.getAttribute("data-tether-longpress");
    if (!action) return;

    var prefix = findPrefix(el);
    if (prefix && action.indexOf(prefix + ".") !== 0) {
      action = prefix + "." + action;
    }

    log("longpress", action);
    Tether.sendEvent("longpress", action, {});
  }

  function cancelLongPress() {
    if (longPressTimer) {
      clearTimeout(longPressTimer);
      longPressTimer = null;
    }
    longPressTarget = null;
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

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
