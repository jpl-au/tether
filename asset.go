package tether

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/jpl-au/fluent/html5/attr/rel"
	"github.com/jpl-au/fluent/html5/link"
	"github.com/jpl-au/fluent/html5/script"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
)

// Asset manages an asset filesystem with content-hashed URLs.
// Construct one as a struct literal and pass it to [StatefulConfig].Assets.
// The first call to [Asset.URL], [Asset.Stylesheet], or [Asset.Script]
// (or handler startup) walks the filesystem and hashes every file.
//
// For embedded assets (single-binary deployments):
//
//	//go:embed static
//	var staticFS embed.FS
//
//	var assets = &tether.Asset{
//	    FS:     staticFS,
//	    Prefix: "/static/",
//	}
//
// For filesystem assets (external, mutable):
//
//	var assets = &tether.Asset{
//	    FS:       os.DirFS("./static"),
//	    Prefix:   "/static/",
//	    WatchDir: "./static",
//	}
//
// When WatchDir is set, the asset manager watches the directory for
// changes and recomputes hashes only for files that change. This
// allows deploying new assets without restarting the server.
type Asset struct {
	// FS is the filesystem containing application assets (CSS, images,
	// JS). Use embed.FS for single-binary deployments or os.DirFS for
	// external assets with live updates. Required.
	FS fs.FS

	// Prefix is the URL path prefix where assets are served. Must end
	// with "/". Defaults to "/assets/" when empty.
	Prefix string

	// Precache lists asset paths (relative to FS) that the service
	// worker should cache on install. These are served with
	// content-hashed query strings for cache-busting. Optional.
	Precache []string

	// WatchDir is the filesystem path to watch for changes. When set,
	// the asset manager uses fsnotify to detect file modifications and
	// recomputes hashes only for changed files. Leave empty for
	// embedded assets (which are immutable). The path should correspond
	// to the directory used in os.DirFS.
	WatchDir string

	once    sync.Once
	prefix  string
	mu      sync.RWMutex      // guards hashes
	hashes  map[string]string // path → 12-char hex hash
	handler http.Handler
	watcher *fsnotify.Watcher
}

// init walks the filesystem and computes per-file content hashes.
// Called lazily via sync.Once on first access. Panics on invalid
// configuration so typos surface at startup. If WatchDir is set,
// starts the filesystem watcher.
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

		a.rehashAll()
		a.handler = http.FileServer(http.FS(a.FS))

		if a.WatchDir != "" {
			a.startWatcher()
		}
	})
}

// rehashAll walks the entire filesystem and computes hashes for all
// files. Called on init and can be called to force a full rehash.
func (a *Asset) rehashAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hashes = make(map[string]string)
	if err := fs.WalkDir(a.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		a.hashes[path] = hashFile(a.FS, path)
		return nil
	}); err != nil {
		panic("tether: failed to walk asset filesystem: " + err.Error())
	}
}

// rehashFile recomputes the hash for a single file. Called by the
// watcher when a file modification is detected.
func (a *Asset) rehashFile(path string) {
	h := hashFile(a.FS, path)
	a.mu.Lock()
	defer a.mu.Unlock()
	if h == "" {
		// File was deleted.
		delete(a.hashes, path)
	} else {
		a.hashes[path] = h
	}
}

// hashFile reads a file and returns its 12-char hex hash. Returns
// empty string if the file cannot be read.
func hashFile(fsys fs.FS, path string) string {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])[:12]
}

// startWatcher creates an fsnotify watcher on WatchDir and
// recomputes hashes when files change.
func (a *Asset) startWatcher() {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		dev.Log().Error("tether: failed to create asset watcher", "dir", a.WatchDir, "error", err)
		return
	}
	a.watcher = w

	// Watch the directory and all subdirectories.
	if err := filepath.Walk(a.WatchDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return w.Add(path)
		}
		return nil
	}); err != nil {
		dev.Log().Error("tether: failed to watch asset directory", "dir", a.WatchDir, "error", err)
		return
	}

	dev.Debug("asset watcher started", "dir", a.WatchDir)

	go func() {
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				switch {
				case event.Has(fsnotify.Write) || event.Has(fsnotify.Create):
					rel, err := filepath.Rel(a.WatchDir, event.Name)
					if err != nil {
						continue
					}
					// Normalise to forward slashes for fs.FS compatibility.
					rel = filepath.ToSlash(rel)
					dev.Debug("asset changed", "path", rel)
					a.rehashFile(rel)
				case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
					rel, err := filepath.Rel(a.WatchDir, event.Name)
					if err != nil {
						continue
					}
					rel = filepath.ToSlash(rel)
					dev.Debug("asset removed", "path", rel)
					a.mu.Lock()
					delete(a.hashes, rel)
					a.mu.Unlock()
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				dev.Log().Error("tether: asset watcher error", "error", err)
			}
		}
	}()
}

// Close stops the filesystem watcher if one is running. Call this
// on shutdown to clean up the watcher goroutine.
func (a *Asset) Close() error {
	if a.watcher != nil {
		return a.watcher.Close()
	}
	return nil
}

// URL returns the hashed URL for the given asset path. The path is
// relative to the asset filesystem root.
//
//	assets.URL("styles.css") // "/static/styles.css?v=a1b2c3d4e5f6"
//
// If the path is not found in the filesystem (typo, read failure),
// the unhashed URL is returned and an error is logged.
func (a *Asset) URL(path string) string {
	a.init()
	if dev.Enabled() && a.WatchDir == "" {
		// Embedded assets in dev mode: recompute per-request so
		// developers using embed.FS with DevMode still get fresh
		// hashes during development.
		data, err := fs.ReadFile(a.FS, path)
		if err != nil {
			dev.Warn("asset not found", "path", path, "error", err)
			return a.prefix + path
		}
		h := sha256.Sum256(data)
		return a.prefix + path + "?v=" + hex.EncodeToString(h[:])[:12]
	}
	a.mu.RLock()
	h, ok := a.hashes[path]
	a.mu.RUnlock()
	if !ok {
		dev.Log().Error("tether: asset not found - check the path and look for earlier read errors", "path", path)
		return a.prefix + path
	}
	return a.prefix + path + "?v=" + h
}

// Stylesheet returns a <link rel="stylesheet"> node for the given
// asset path with a content-hashed URL.
func (a *Asset) Stylesheet(path string) node.Node {
	return link.New().Rel(rel.Stylesheet).Href(a.URL(path))
}

// Script returns a <script> node for the given asset path with a
// content-hashed URL.
func (a *Asset) Script(path string) node.Node {
	return script.New().Src(a.URL(path))
}

// hash returns a single hash representing all files in the asset
// filesystem. Used to mix into the service worker CACHE_VERSION.
func (a *Asset) hash() string {
	a.init()
	a.mu.RLock()
	defer a.mu.RUnlock()
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

// mountAssets creates an [assetMount] for each [Asset].
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

// cacheHandler sets Cache-Control headers based on dev mode.
func cacheHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		switch {
		case dev.Enabled():
			w.Header().Set("Cache-Control", "no-store")
		case r.URL.Query().Get("v") != "":
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		h.ServeHTTP(w, r)
	})
}
