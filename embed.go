package poly

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"sync"
)

// clientFS embeds the client-side JS runtime and the idiomorph library.
// These files are served at the /_poly/ path by the Handler.
//
//go:embed client/fluent-poly.js client/idiomorph.min.js client/fluent-poly-worker.js client/fluent-poly-push-worker.js client/fluent-poly-upload.js
var clientFS embed.FS

// clientFiles returns an fs.FS rooted at the client/ directory so that
// file paths served by http.FileServer match the expected URL paths
// (e.g. /fluent-poly.js rather than /client/fluent-poly.js).
func clientFiles() fs.FS {
	sub, err := fs.Sub(clientFS, "client")
	if err != nil {
		// The embed directive guarantees the "client" directory exists.
		// If this fails, the binary is corrupted.
		panic("poly: embedded client files missing: " + err.Error())
	}
	return sub
}

var (
	clientVersionOnce sync.Once
	clientVersionVal  string
)

// clientVersion returns a 12-character hex hash of the embedded client
// files. The hash is computed once and cached — the embedded content is
// immutable for the lifetime of the process. Used for cache-busting
// query strings on script tags and as the base for the service worker
// CACHE_VERSION.
func clientVersion() string {
	clientVersionOnce.Do(func() {
		h := sha256.New()
		fs.WalkDir(clientFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, _ := fs.ReadFile(clientFS, path)
			h.Write(data)
			return nil
		})
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
		h.Write([]byte(a.contentHash()))
		precache = append(precache, a.precacheURLs()...)
	}
	version := hex.EncodeToString(h.Sum(nil))[:12]

	raw, _ := fs.ReadFile(clientFiles(), "fluent-poly-worker.js")
	body := bytes.Replace(raw,
		[]byte(`"poly-v1"`),
		[]byte(`"poly-`+version+`"`), 1)

	if len(precache) > 0 {
		extra, _ := json.Marshal(precache)
		body = bytes.Replace(body,
			[]byte("var PRECACHE_EXTRA = [];"),
			[]byte("var PRECACHE_EXTRA = "+string(extra)+";"), 1)
	}

	return body
}

// ServeClient returns an http.Handler that serves the embedded client
// JS runtime (fluent-poly.js, idiomorph, service worker). Mount it at
// /_poly/ when the poly handler is not at the root path:
//
//	mux.Handle("/_poly/", http.StripPrefix("/_poly/", poly.ServeClient()))
//
// When the poly handler IS mounted at "/" the client runtime is
// served automatically and this function is not needed.
func ServeClient() http.Handler {
	return newClientHandler(nil)
}

// newClientHandler builds an http.Handler that serves the embedded
// client runtime. The Handler mounts this at /_poly/ so the HTML page
// can load fluent-poly.js and idiomorph. The service worker script
// gets special treatment: its CACHE_VERSION is set to a content hash
// of the embedded files, and a Service-Worker-Allowed header is added
// so it can control the entire origin.
func newClientHandler(assets []*Asset) http.Handler {
	fileServer := http.FileServer(http.FS(clientFiles()))

	var workerOnce sync.Once
	var workerBody []byte

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The service worker needs the content-hash cache version
		// injected and the scope header set, so it is served directly
		// rather than through the static file server.
		if r.URL.Path == "/fluent-poly-worker.js" || r.URL.Path == "fluent-poly-worker.js" {
			// Defence-in-depth: reject cross-origin requests for the
			// worker script. The browser already prevents cross-origin
			// registration, but this guards against misconfigured proxies.
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || stripPort(u.Host) != stripPort(r.Host) {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
			}
			workerOnce.Do(func() {
				workerBody = buildWorkerJS(assets)
			})
			w.Header().Set("Service-Worker-Allowed", "/")
			w.Header().Set("Content-Type", "application/javascript")
			w.Write(workerBody)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
