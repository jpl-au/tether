// fluent-tether-upload.js — file upload extension for Fluent Tether.
//
// Loaded automatically when any element uses data-tether-upload. Handles
// file selection, multipart POST to the server, and progress tracking
// via signals. Uses XMLHttpRequest for upload progress events.

(function () {
  "use strict";

  var root = null;
  var endpoint = "";
  var sessionID = "";

  function init() {
    root = document.querySelector("[data-tether-root]");
    if (!root) return;

    endpoint = root.getAttribute("data-tether-endpoint") || "";
    sessionID = root.getAttribute("data-tether-session") || "";

    bindUploads(root);
  }

  // bindUploads attaches listeners to all upload triggers within a
  // subtree. Called on init and after each morph to pick up new elements.
  function bindUploads(container) {
    var els = container.querySelectorAll("[data-tether-upload]");
    for (var i = 0; i < els.length; i++) {
      setupUpload(els[i]);
    }
  }

  // setupUpload wires a single upload element. File inputs trigger on
  // change; buttons and other elements trigger on click and look for a
  // file input in the closest form or parent.
  function setupUpload(el) {
    // Guard against double-binding after morphs.
    if (el.hasAttribute("data-tether-upload-bound")) return;
    el.setAttribute("data-tether-upload-bound", "");

    var action = el.getAttribute("data-tether-upload");
    var isFileInput = el.tagName === "INPUT" && el.type === "file";

    if (isFileInput) {
      el.addEventListener("change", function () {
        if (el.files && el.files.length > 0) {
          uploadFiles(action, el.files, el);
        }
      });
    } else {
      el.addEventListener("click", function (e) {
        // If data-tether-upload-input is set, use it as a CSS selector
        // to find file inputs anywhere in the document. This supports
        // layouts where the trigger button is distant from the file
        // input (e.g. in a different part of a modal).
        var selector = el.getAttribute("data-tether-upload-input");
        var inputs;
        if (selector) {
          inputs = document.querySelectorAll(selector);
        } else {
          // Default: find file inputs in the closest form, or as siblings.
          var form = el.closest("form");
          inputs = form
            ? form.querySelectorAll('input[type="file"]')
            : el.parentElement
              ? el.parentElement.querySelectorAll('input[type="file"]')
              : [];
        }

        var files = collectFiles(inputs);
        if (files.length === 0) return;

        e.preventDefault();
        uploadFiles(action, files, el);
      });
    }
  }

  // collectFiles gathers all selected files from a set of file inputs.
  function collectFiles(inputs) {
    var files = [];
    for (var i = 0; i < inputs.length; i++) {
      if (inputs[i].files) {
        for (var j = 0; j < inputs[i].files.length; j++) {
          files.push(inputs[i].files[j]);
        }
      }
    }
    return files;
  }

  // uploadFiles sends files to the server via multipart POST and
  // tracks progress through tether signals.
  function uploadFiles(action, files, triggerEl) {
    var formData = new FormData();
    for (var i = 0; i < files.length; i++) {
      formData.append("file", files[i]);
    }

    var xhr = new XMLHttpRequest();

    // Progress signal: 0–100.
    Tether.setSignal("upload:" + action + ":progress", "0");
    Tether.setSignal("upload:" + action + ":state", "uploading");

    xhr.upload.addEventListener("progress", function (e) {
      if (e.lengthComputable) {
        var pct = Math.round((e.loaded / e.total) * 100);
        Tether.setSignal("upload:" + action + ":progress", String(pct));
      }
    });

    xhr.addEventListener("load", function () {
      if (xhr.status >= 200 && xhr.status < 300) {
        Tether.setSignal("upload:" + action + ":progress", "100");
        Tether.setSignal("upload:" + action + ":state", "done");
      } else {
        Tether.setSignal("upload:" + action + ":state", "error");
      }
    });

    xhr.addEventListener("error", function () {
      Tether.setSignal("upload:" + action + ":state", "error");
    });

    xhr.addEventListener("abort", function () {
      Tether.setSignal("upload:" + action + ":state", "idle");
    });

    // Read the session ID fresh from the root element each time.
    // After reconnection the ID may have changed.
    var sid = root ? root.getAttribute("data-tether-session") : sessionID;
    xhr.open("POST", endpoint);
    xhr.setRequestHeader("X-Tether-Session", sid);
    xhr.setRequestHeader("X-Tether-Upload", action);
    xhr.send(formData);
  }

  // Re-bind after server updates. The core runtime fires tether:update
  // when it finishes applying patches and morphs.
  document.addEventListener("tether:update", function (e) {
    var target = e.detail && e.detail.root ? e.detail.root : root;
    if (target) bindUploads(target);
  });

  // Initialise when the DOM is ready. If the document is already
  // loaded (extension script loaded after DOMContentLoaded), run
  // immediately.
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
