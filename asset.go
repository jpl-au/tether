package tether

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/jpl-au/fluent/html5/attr/rel"
	"github.com/jpl-au/fluent/html5/link"
	"github.com/jpl-au/fluent/html5/script"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
)

// Asset manages an embedded asset filesystem with content-hashed URLs.
// Construct one as a struct literal and pass it to [StatefulConfig].Assets.
// The first call to [Asset.URL], [Asset.Stylesheet], or [Asset.Script]
// (or handler startup) walks the filesystem and hashes every file.
//
//	//go:embed static
//	var staticFS embed.FS
//
//	var assets = &tether.Asset{
//	    FS:     staticFS,
//	    Prefix: "/static/",
//	}
//
//	// In your Layout:
//	assets.Stylesheet("styles.css") // <link rel="stylesheet" href="/static/styles.css?v=a1b2c3d4e5f6">
type Asset struct {
	// FS is the filesystem containing application assets (CSS, images,
	// JS). Use embed.FS for single-binary deployments or os.DirFS for
	// development with live reloading. Required.
	FS fs.FS

	// Prefix is the URL path prefix where assets are served. Must end
	// with "/". Defaults to "/assets/" when empty.
	Prefix string

	// Precache lists asset paths (relative to FS) that the service
	// worker should cache on install. These are served with
	// content-hashed query strings for cache-busting. Optional.
	Precache []string

	once    sync.Once
	prefix  string
	hashes  map[string]string // path → 12-char hex hash
	handler http.Handler
}

// init walks the filesystem and computes per-file content hashes.
// Called lazily via sync.Once on first access. Panics on invalid
// configuration so typos surface at startup.
func (a *Asset) init() {
	a.once.Do(func() {
		if a.FS == nil {
			panic("tether: Asset.FS is required")
		}

		a.prefix = a.Prefix
		if a.prefix == "" {
			a.prefix = "/assets/"
		}
		if !strings.HasSuffix(a.prefix, "/") {
			panic("tether: Asset.Prefix must end with \"/\"")
		}

		a.hashes = make(map[string]string)
		if err := fs.WalkDir(a.FS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(a.FS, path)
			if err != nil {
				slog.Error("failed to read asset file", "path", path, "err", err)
				return nil
			}
			h := sha256.Sum256(data)
			a.hashes[path] = hex.EncodeToString(h[:])[:12]
			return nil
		}); err != nil {
			panic("tether: failed to walk asset filesystem: " + err.Error())
		}

		a.handler = http.FileServer(http.FS(a.FS))
	})
}

// URL returns the hashed URL for the given asset path. The path is
// relative to the asset filesystem root.
//
//	assets.URL("styles.css") // "/static/styles.css?v=a1b2c3d4e5f6"
//
// If the path is not found in the filesystem (typo, read failure),
// the unhashed URL is returned and an error is logged. The asset
// file server will still serve the file - only cache-busting is
// lost.
func (a *Asset) URL(path string) string {
	a.init()
	if dev.Enabled() {
		// Recompute the hash on every call so edited files get fresh
		// cache-busting parameters without a server restart. Slightly
		// slower per request but correctness matters more than speed
		// during development.
		data, err := fs.ReadFile(a.FS, path)
		if err != nil {
			dev.Warn("asset not found", "path", path, "error", err)
			return a.prefix + path
		}
		h := sha256.Sum256(data)
		return a.prefix + path + "?v=" + hex.EncodeToString(h[:])[:12]
	}
	h, ok := a.hashes[path]
	if !ok {
		slog.Error("tether: asset not found - check the path and look for earlier read errors", "path", path)
		return a.prefix + path
	}
	return a.prefix + path + "?v=" + h
}

// Stylesheet returns a <link rel="stylesheet"> node for the given
// asset path with a content-hashed URL.
//
//	assets.Stylesheet("styles.css")
//	// <link rel="stylesheet" href="/static/styles.css?v=a1b2c3d4e5f6">
func (a *Asset) Stylesheet(path string) node.Node {
	return link.New().Rel(rel.Stylesheet).Href(a.URL(path))
}

// Script returns a <script> node for the given asset path with a
// content-hashed URL.
//
//	assets.Script("app.js")
//	// <script src="/static/app.js?v=a1b2c3d4e5f6"></script>
func (a *Asset) Script(path string) node.Node {
	return script.New().Src(a.URL(path))
}

// hash returns a single hash representing all files in the asset
// filesystem. Used to mix into the service worker CACHE_VERSION.
// Keys are sorted so the result is deterministic across restarts.
func (a *Asset) hash() string {
	a.init()
	h := sha256.New()
	for _, path := range slices.Sorted(maps.Keys(a.hashes)) {
		h.Write([]byte(path))
		h.Write([]byte(a.hashes[path]))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// precacheURLs returns the hashed URLs for all precache entries.
func (a *Asset) precacheURLs() []string {
	a.init()
	urls := make([]string, len(a.Precache))
	for i, path := range a.Precache {
		urls[i] = a.URL(path)
	}
	return urls
}

// mountAssets creates an [assetMount] for each [Asset], wrapping
// the file server with cache headers that respect [dev.Enabled] at
// request time.
func (app *App) mountAssets() []assetMount {
	mounts := make([]assetMount, len(app.Assets))
	for i, a := range app.Assets {
		a.init()
		handler := http.StripPrefix(a.prefix, a.handler)
		handler = cacheHandler(handler)
		mounts[i] = assetMount{prefix: a.prefix, handler: handler}
	}
	return mounts
}

// cacheHandler sets Cache-Control headers based on dev mode. In dev
// mode, assets are served with no-store so the browser always fetches
// fresh copies. In production, versioned assets (?v=…) get immutable
// cache headers.
func cacheHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if dev.Enabled() {
			w.Header().Set("Cache-Control", "no-store")
		} else if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		h.ServeHTTP(w, r)
	})
}
