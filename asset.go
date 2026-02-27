package poly

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/jpl-au/fluent/html5/attr/rel"
	"github.com/jpl-au/fluent/html5/link"
	"github.com/jpl-au/fluent/html5/script"
	"github.com/jpl-au/fluent/node"
)

// Asset manages an embedded asset filesystem with content-hashed URLs.
// Construct one as a struct literal and pass it to [Config].Assets.
// The first call to [Asset.URL], [Asset.Stylesheet], or [Asset.Script]
// (or handler startup) walks the filesystem and hashes every file.
//
//	//go:embed static
//	var staticFS embed.FS
//
//	var assets = &poly.Asset{
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
			panic("poly: Asset.FS is required")
		}

		a.prefix = a.Prefix
		if a.prefix == "" {
			a.prefix = "/assets/"
		}
		if !strings.HasSuffix(a.prefix, "/") {
			panic("poly: Asset.Prefix must end with \"/\"")
		}

		a.hashes = make(map[string]string)
		fs.WalkDir(a.FS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, _ := fs.ReadFile(a.FS, path)
			h := sha256.Sum256(data)
			a.hashes[path] = hex.EncodeToString(h[:])[:12]
			return nil
		})

		a.handler = http.FileServer(http.FS(a.FS))
	})
}

// URL returns the hashed URL for the given asset path. The path is
// relative to the asset filesystem root.
//
//	assets.URL("styles.css") // "/static/styles.css?v=a1b2c3d4e5f6"
//
// Panics if the path does not exist in the filesystem — this catches
// typos at startup rather than serving broken links at runtime.
func (a *Asset) URL(path string) string {
	a.init()
	h, ok := a.hashes[path]
	if !ok {
		panic(fmt.Sprintf("poly: asset %q not found in filesystem", path))
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

// contentHash returns a single hash representing all files in the
// asset filesystem. Used to mix into the service worker CACHE_VERSION.
func (a *Asset) contentHash() string {
	a.init()
	h := sha256.New()
	for path, hash := range a.hashes {
		h.Write([]byte(path))
		h.Write([]byte(hash))
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

// buildAssetMounts creates an [assetMount] for each [Asset], wrapping
// the file server with DevMode-aware cache headers.
func buildAssetMounts(assets []*Asset, devMode bool) []assetMount {
	mounts := make([]assetMount, len(assets))
	for i, a := range assets {
		a.init()
		handler := http.StripPrefix(a.prefix, a.handler)
		if devMode {
			handler = noCacheHandler(handler)
		} else {
			handler = immutableCacheHandler(handler)
		}
		mounts[i] = assetMount{prefix: a.prefix, handler: handler}
	}
	return mounts
}

// noCacheHandler wraps h with Cache-Control: no-store for DevMode.
func noCacheHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// immutableCacheHandler sets long-lived cache headers on responses
// when the request URL contains a ?v= content hash.
func immutableCacheHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		h.ServeHTTP(w, r)
	})
}
