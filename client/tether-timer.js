// tether-timer.js - client-side timer extension for Tether.
//
// Loaded automatically when any element uses data-tether-timer (see
// bind.Timer). Timers tick entirely in the browser; the server
// controls them by pushing signals: "name.running" (boolean)
// starts/pauses, and setting "name" to a number resets the value.
// The element's text content is updated with the formatted time on
// each tick - no BindText needed.

(function () {
  "use strict";

  var timers = {}; // keyed by timer name

  // scan finds all data-tether-timer elements and registers them.
  // Runs on load and after each server update so dynamically added
  // timers are picked up and morph-replaced elements re-register.
  function scan() {
    var els = document.querySelectorAll("[data-tether-timer]");
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      var name = el.getAttribute("data-tether-timer");
      if (!name) continue;
      if (timers[name] && timers[name].el === el) continue;

      // Stop any existing timer with this name before re-registering
      // (element may have been replaced by a morph).
      if (timers[name] && timers[name].interval) {
        clearInterval(timers[name].interval);
      }

      var countdown = parseFloat(el.getAttribute("data-tether-timer-countdown"));
      var precision = parseInt(el.getAttribute("data-tether-timer-precision")) || 1000;
      var format = el.getAttribute("data-tether-timer-format") || "auto";
      var complete = el.getAttribute("data-tether-timer-complete") || "";

      var t = {
        el: el,
        name: name,
        down: countdown > 0,
        start: countdown > 0 ? countdown : 0,
        precision: precision,
        format: format,
        complete: complete,
        interval: null
      };

      timers[name] = t;

      // Initialise the signal value if not already set by the server.
      if (Tether.getSignal(name) === undefined || Tether.getSignal(name) === null) {
        Tether.signals[name] = t.start;
      }

      // Display the initial formatted value.
      el.textContent = formatTimer(Tether.getSignal(name), t);

      // If the running signal is already truthy (e.g. server pushed
      // it before the element appeared), start ticking immediately.
      if (Tether.isTruthy(Tether.getSignal(name + ".running"))) {
        startTimer(t);
      }
    }
  }

  // startTimer begins ticking. Each tick pushes the new value through
  // Tether.setSignal, which refreshes bindings and notifies signal
  // listeners - including this extension's own listener, which
  // renders the element text. Count-down timers reaching zero clear
  // the interval and send an optional completion event to the server.
  function startTimer(t) {
    if (t.interval) return; // already running
    t.interval = setInterval(function () {
      // The element may have been morphed away since the last tick.
      // Stop ticking (and never fire "complete" on a dead element);
      // if a timer with this name reappears, scan re-registers it and
      // the running signal restarts it.
      if (!t.el.isConnected) {
        clearInterval(t.interval);
        t.interval = null;
        return;
      }
      var step = t.precision / 1000;
      var value = Tether.getSignal(t.name);
      var current = typeof value === "number" ? value : 0;

      if (t.down) {
        current -= step;
        if (current <= 0) {
          Tether.setSignal(t.name, 0);
          stopTimer(t);
          // Fire completion event to the server.
          if (t.complete) {
            Tether.sendEvent("click", t.complete, {});
          }
          return;
        }
      } else {
        current += step;
      }

      Tether.setSignal(t.name, current);
    }, t.precision);
  }

  // stopTimer clears the interval and marks the running signal as
  // false so BindShow/BindHide elements react immediately even if the
  // server does not explicitly push false.
  function stopTimer(t) {
    if (t.interval) {
      clearInterval(t.interval);
      t.interval = null;
    }
    Tether.setSignal(t.name + ".running", false);
  }

  // onSignal reacts to timer control signals from any source - server
  // pushes, client signal actions, and this extension's own ticks.
  function onSignal(key, value) {
    // "name.running" starts or pauses the named timer.
    if (key.length > 8 && key.indexOf(".running") === key.length - 8) {
      var t = timers[key.substring(0, key.length - 8)];
      if (!t) return;
      if (Tether.isTruthy(value)) {
        startTimer(t);
      } else if (t.interval) {
        clearInterval(t.interval);
        t.interval = null;
      }
      return;
    }

    // Direct value set (tick, reset to 0, or server-set value):
    // render the formatted time into the element.
    var t = timers[key];
    if (t && typeof value === "number") {
      t.el.textContent = formatTimer(value, t);
    }
  }

  // formatTimer renders a seconds value using the timer's format.
  function formatTimer(totalSeconds, t) {
    var neg = totalSeconds < 0;
    var s = Math.abs(totalSeconds);
    var fmt = t.format;

    if (fmt === "auto") {
      if (s < 60) fmt = "ss";
      else if (s < 3600) fmt = "mm:ss";
      else fmt = "hh:mm:ss";
    }

    var hours = Math.floor(s / 3600);
    var minutes = Math.floor((s % 3600) / 60);
    var seconds = Math.floor(s % 60);
    var frac = s - Math.floor(s);

    var result = "";

    switch (fmt) {
      case "hh:mm:ss":
        result = pad(hours) + ":" + pad(minutes) + ":" + pad(seconds);
        break;
      case "mm:ss":
        // Roll hours into minutes for mm:ss format.
        result = pad(hours * 60 + minutes) + ":" + pad(seconds);
        break;
      case "ss":
        result = String(Math.floor(s));
        break;
      case "mm:ss.S":
        result = pad(hours * 60 + minutes) + ":" + pad(seconds) + "." + Math.floor(frac * 10);
        break;
      case "mm:ss.SS":
        result = pad(hours * 60 + minutes) + ":" + pad(seconds) + "." + pad(Math.floor(frac * 100));
        break;
      default:
        // Unknown format - fall back to auto.
        if (s < 60) result = String(Math.floor(s));
        else if (s < 3600) result = pad(minutes) + ":" + pad(seconds);
        else result = pad(hours) + ":" + pad(minutes) + ":" + pad(seconds);
    }

    return neg ? "-" + result : result;
  }

  function pad(n) {
    return n < 10 ? "0" + n : String(n);
  }

  function init() {
    scan();
    Tether.onUpdate(scan);
    Tether.onSignalChange(onSignal);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
