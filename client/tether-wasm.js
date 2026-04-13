// tether-wasm.js bootstraps a Go WASM client runtime for tether.
//
// The framework injects this script automatically when the app uses
// tether.Runtime.WASM(). It reads configuration from data attributes
// on the tether root element:
//
//   data-tether-wasm-src   Path to the compiled .wasm blob (required).
//
// The wasm_exec.js support script is loaded by convention from the
// same directory as the WASM blob. For example, if the blob is at
// /static/client.go.wasm, the loader fetches /static/wasm_exec.js.
(function () {
  "use strict";

  var root = document.querySelector("[data-tether-root]");
  if (!root) {
    console.error("tether-wasm: no [data-tether-root] element found");
    return;
  }

  var wasmSrc = root.getAttribute("data-tether-wasm-src");
  if (!wasmSrc) {
    console.error("tether-wasm: missing data-tether-wasm-src attribute");
    return;
  }

  // Derive wasm_exec.js path from the WASM blob's directory.
  var dir = wasmSrc.substring(0, wasmSrc.lastIndexOf("/") + 1);
  var execSrc = dir + "wasm_exec.js";

  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      var s = document.createElement("script");
      s.src = src;
      s.onload = resolve;
      s.onerror = function () {
        reject(new Error("tether-wasm: failed to load " + src));
      };
      document.head.appendChild(s);
    });
  }

  loadScript(execSrc)
    .then(function () {
      var go = new Go();
      return WebAssembly.instantiateStreaming(
        fetch(wasmSrc),
        go.importObject
      ).then(function (result) {
        console.log("tether-wasm: starting runtime");
        go.run(result.instance);
      });
    })
    .catch(function (err) {
      console.error("tether-wasm: boot failed:", err);
    });
})();
