package tether

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/jpl-au/tether/dev"
)

// clientFS embeds the client-side JS runtime and the idiomorph library.
// The readable sources under client/ are the authoritative, dev-mode
// asset; the client/dist tree holds the minified and precompressed
// variants that production serves, generated from the sources by
// gen_client.go. Both are embedded so the process can serve either
// depending on dev mode. All are served under the /_tether/ path.
//
//go:generate go run gen_client.go
//go:embed client/tether.js client/idiomorph.min.js client/tether-worker.js client/tether-push-worker.js client/tether-upload.js client/tether-drag-and-drop.js client/tether-touch.js client/tether-hotkey.js client/tether-timer.js client/tether-select.js client/tether-template.js client/tether-wasm.js client/tether-wire-cbor.js
//go:embed client/dist
var clientFS embed.FS

// clientFiles returns an fs.FS rooted at the client/ directory so
// callers can read an embedded script by its bare name (tether.js
// rather than client/tether.js).
func clientFiles() fs.FS {
	sub, err := fs.Sub(clientFS, "client")
	if err != nil {
		// The embed directive guarantees the "client" directory exists.
		// If this fails, the binary is corrupted.
		panic("tether: embedded client files missing: " + err.Error())
	}
	return sub
}

var (
	clientVersionOnce sync.Once
	clientVersionVal  string
)

// clientVersion returns a 12-character hex hash of the embedded client
// files. The hash is computed once and cached - the embedded content is
// immutable for the lifetime of the process. Used for cache-busting
// query strings on script tags and as the base for the service worker
// CACHE_VERSION.
func clientVersion() string {
	clientVersionOnce.Do(func() {
		h := sha256.New()
		err := fs.WalkDir(clientFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(clientFS, path)
			if err != nil {
				dev.Log().Error("tether: failed to read embedded file", "path", path, "error", err)
				return nil
			}
			h.Write(data)
			return nil
		})
		if err != nil {
			dev.Log().Error("tether: failed to walk embedded client files", "error", err)
		}
		clientVersionVal = hex.EncodeToString(h.Sum(nil))[:12]
	})
	return clientVersionVal
}

// workerTemplate returns the service-worker template to inject into.
// Production serves the minified template from client/dist; dev mode
// serves the readable source so the worker is debuggable.
func workerTemplate() []byte {
	name := "tether-worker.js"
	if !dev.Enabled() {
		name = "dist/tether-worker.js"
	}
	raw, err := fs.ReadFile(clientFiles(), name)
	if err != nil {
		panic("tether: failed to read embedded worker script: " + err.Error())
	}
	return raw
}

// buildWorkerJS injects the cache version and precache list into the
// given service-worker template. The version hash is derived from the
// embedded client files and any application assets, so the browser's
// service worker update check fires whenever the library is rebuilt or
// any precached asset changes.
//
// The template is either the readable source or its minified form; the
// injection targets both spellings of the precache placeholder so the
// same routine works in dev and production.
func buildWorkerJS(assets []*Asset, template []byte) []byte {
	// Start from the base client hash and mix in each asset collection
	// so the worker version changes when any asset content changes.
	h := sha256.New()
	h.Write([]byte(clientVersion()))
	var precache []string
	for _, a := range assets {
		h.Write([]byte(a.hash()))
		precache = append(precache, a.precacheURLs()...)
	}
	version := hex.EncodeToString(h.Sum(nil))[:12]

	// The version lives in a string literal, which survives minification
	// unchanged, so one target matches both templates.
	body := bytes.Replace(template,
		[]byte(`"tether-v1"`),
		[]byte(`"tether-`+version+`"`), 1)

	if len(precache) > 0 {
		extra, err := json.Marshal(precache)
		if err != nil {
			panic("tether: failed to marshal precache URLs: " + err.Error())
		}
		// The readable source declares the placeholder on its own line;
		// the minifier folds it into a merged var statement and drops the
		// spacing and semicolon. Only one of these forms is present in a
		// given template, and each keeps its own surrounding syntax.
		replacements := []struct{ target, with []byte }{
			{[]byte("var PRECACHE_EXTRA = [];"), []byte("var PRECACHE_EXTRA = " + string(extra) + ";")},
			{[]byte("PRECACHE_EXTRA=[]"), []byte("PRECACHE_EXTRA=" + string(extra))},
		}
		for _, r := range replacements {
			if bytes.Contains(body, r.target) {
				body = bytes.Replace(body, r.target, r.with, 1)
				break
			}
		}
	}

	return body
}

// ServeClient returns an http.Handler that serves the embedded client
// JS runtime (tether.js, idiomorph, service worker). Mount it at
// /_tether/ when the tether handler is not at the root path:
//
//	mux.Handle("/_tether/", http.StripPrefix("/_tether/", tether.ServeClient()))
//
// When the tether handler IS mounted at "/" the client runtime is
// served automatically and this function is not needed.
func ServeClient() http.Handler {
	a := &App{}
	return a.jsHandler()
}

// jsHandler builds an http.Handler that serves the embedded client
// runtime. The Handler mounts this at /_tether/ so the HTML page can
// load tether.js and idiomorph.
//
// Production serves the minified, precompressed assets from client/dist
// with Accept-Encoding negotiation (brotli, then gzip, then identity).
// Dev mode instead serves the readable, unminified sources so developers
// can set breakpoints and read stack traces against real code.
//
// The service worker gets special treatment in both modes: its
// CACHE_VERSION is set to a content hash of the embedded files, and a
// Service-Worker-Allowed header permits the client to register the
// worker at any scope (the client scopes to the handler's endpoint via
// data-tether-endpoint). Because its body is assembled per application
// it is compressed once, at first request, rather than by go generate.
func (a *App) jsHandler() http.Handler {
	var workerOnce sync.Once
	var workerAsset clientAsset

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")

		if dev.Enabled() {
			a.serveDevClient(w, r, p)
			return
		}

		if p == clientWorker {
			workerOnce.Do(func() {
				workerAsset = compressAsset(buildWorkerJS(a.Assets, workerTemplate()))
			})
			w.Header().Set("Service-Worker-Allowed", "/")
			writeClientAsset(w, r, workerAsset)
			return
		}

		asset, ok := clientAssets()[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if p == clientPushWorker {
			w.Header().Set("Service-Worker-Allowed", "/")
		}
		writeClientAsset(w, r, asset)
	})
}

// serveDevClient serves the readable, unminified source for a client
// path uncompressed. Only flat file names embedded under client/ are
// served, so requests can neither reach the dist tree nor traverse out
// of it.
func (a *App) serveDevClient(w http.ResponseWriter, r *http.Request, p string) {
	if p == clientWorker {
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Content-Type", jsContentType)
		if _, err := w.Write(buildWorkerJS(a.Assets, workerTemplate())); err != nil {
			dev.Log().Warn("tether: failed to write worker script", "err", err)
		}
		return
	}

	if strings.Contains(p, "/") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(clientFiles(), p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if p == clientPushWorker {
		w.Header().Set("Service-Worker-Allowed", "/")
	}
	w.Header().Set("Content-Type", jsContentType)
	if _, err := w.Write(data); err != nil {
		dev.Log().Warn("tether: failed to write client asset", "err", err)
	}
}
