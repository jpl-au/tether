package tether

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"

	"github.com/jpl-au/tether/dev"
)

// clientFS embeds the client-side JS runtime and the idiomorph library.
// These files are served at the /_tether/ path by the Handler.
//
//go:embed client/tether.js client/idiomorph.min.js client/tether-worker.js client/tether-push-worker.js client/tether-upload.js
var clientFS embed.FS

// clientFiles returns an fs.FS rooted at the client/ directory so that
// file paths served by http.FileServer match the expected URL paths
// (e.g. /tether.js rather than /client/tether.js).
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

// buildWorkerJS returns the service worker JS with the cache version
// derived from the embedded client files and any application assets.
// The version hash and precache URLs are injected at serve time so the
// browser's service worker update check detects changes whenever the
// library is rebuilt or any precached asset changes.
func buildWorkerJS(assets []*Asset) []byte {
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

	raw, err := fs.ReadFile(clientFiles(), "tether-worker.js")
	if err != nil {
		panic("tether: failed to read embedded worker script: " + err.Error())
	}
	body := bytes.Replace(raw,
		[]byte(`"tether-v1"`),
		[]byte(`"tether-`+version+`"`), 1)

	if len(precache) > 0 {
		extra, err := json.Marshal(precache)
		if err != nil {
			panic("tether: failed to marshal precache URLs: " + err.Error())
		}
		body = bytes.Replace(body,
			[]byte("var PRECACHE_EXTRA = [];"),
			[]byte("var PRECACHE_EXTRA = "+string(extra)+";"), 1)
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

// jsHandler builds an http.Handler that serves the embedded
// client runtime. The Handler mounts this at /_tether/ so the HTML page
// can load tether.js and idiomorph. The service worker script
// gets special treatment: its CACHE_VERSION is set to a content hash
// of the embedded files, and a Service-Worker-Allowed header permits
// the client to register the worker at any scope (the client scopes
// to the handler's endpoint via data-tether-endpoint).
func (app *App) jsHandler() http.Handler {
	fileServer := http.FileServer(http.FS(clientFiles()))

	var workerOnce sync.Once
	var workerBody []byte
	var pushWorkerOnce sync.Once
	var pushWorkerBody []byte

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The service worker needs the content-hash cache version
		// injected and the scope header set, so it is served directly
		// rather than through the static file server.
		if r.URL.Path == "/tether-worker.js" || r.URL.Path == "tether-worker.js" {
			workerOnce.Do(func() {
				workerBody = buildWorkerJS(app.Assets)
			})
			w.Header().Set("Service-Worker-Allowed", "/")
			w.Header().Set("Content-Type", "application/javascript")
			if _, err := w.Write(workerBody); err != nil {
				dev.Log().Warn("failed to write worker script", "err", err)
			}
			return
		}
		// The push-only worker also needs the scope header so it can
		// be registered at the handler's endpoint (e.g. /app/) rather
		// than being restricted to /_tether/.
		if r.URL.Path == "/tether-push-worker.js" || r.URL.Path == "tether-push-worker.js" {
			pushWorkerOnce.Do(func() {
				raw, err := fs.ReadFile(clientFiles(), "tether-push-worker.js")
				if err != nil {
					panic("tether: failed to read embedded push worker script: " + err.Error())
				}
				pushWorkerBody = raw
			})
			w.Header().Set("Service-Worker-Allowed", "/")
			w.Header().Set("Content-Type", "application/javascript")
			if _, err := w.Write(pushWorkerBody); err != nil {
				dev.Log().Warn("failed to write push worker script", "err", err)
			}
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
