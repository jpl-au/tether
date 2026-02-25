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
// These files are served by ServeClient() at the /_poly/ mount point.
//
//go:embed client/fluent-poly.js client/idiomorph.min.js client/poly-worker.js
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

// buildWorkerJS returns the service worker JS with the cache version set
// to a content hash of the embedded client files and any extra precache
// URLs injected. The version is injected at serve time so the browser's
// service worker update check detects changes whenever the library is
// rebuilt with new client code.
func buildWorkerJS(precache []string) []byte {
	h := sha256.New()
	fs.WalkDir(clientFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := fs.ReadFile(clientFS, path)
		h.Write(data)
		return nil
	})
	version := hex.EncodeToString(h.Sum(nil))[:12]

	raw, _ := fs.ReadFile(clientFiles(), "poly-worker.js")
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

// ServeClient serves the embedded client runtime. Mount at /_poly/ so the
// HTML page can load fluent-poly.js and idiomorph. The handler adds a
// Service-Worker-Allowed header when serving poly-worker.js so the service
// worker can control the entire origin despite being served from /_poly/.
//
// The service worker's CACHE_VERSION is set to a content hash of the
// embedded files so cache invalidation happens automatically when the
// library is rebuilt with new client code.
//
// The optional precache parameter lists additional app-specific asset URLs
// (e.g. "/styles.css", "/logo.svg") that the service worker should cache
// on install alongside the poly runtime files.
func ServeClient(precache ...string) http.Handler {
	fileServer := http.FileServer(http.FS(clientFiles()))

	var workerOnce sync.Once
	var workerBody []byte

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The service worker needs the content-hash cache version
		// injected and the scope header set, so it is served directly
		// rather than through the static file server.
		if r.URL.Path == "/poly-worker.js" || r.URL.Path == "poly-worker.js" {
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
				workerBody = buildWorkerJS(precache)
			})
			w.Header().Set("Service-Worker-Allowed", "/")
			w.Header().Set("Content-Type", "application/javascript")
			w.Write(workerBody)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
