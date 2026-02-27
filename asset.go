package poly

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/jpl-au/fluent/html5/attr/rel"
	"github.com/jpl-au/fluent/html5/link"
	"github.com/jpl-au/fluent/html5/script"
	"github.com/jpl-au/fluent/node"
)

// AssetConfig configures an embedded asset collection. Use [NewAsset]
// to create an [Asset] from this configuration.
type AssetConfig struct {
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
}

// Asset manages an embedded asset filesystem with content-hashed URLs.
// Create one with [NewAsset], then use [Asset.Stylesheet],
// [Asset.Script], or [Asset.URL] to reference assets in templates.
//
//	//go:embed static
//	var staticFS embed.FS
//
//	var assets = poly.NewAsset(poly.AssetConfig{
//	    FS:     staticFS,
//	    Prefix: "/static/",
//	})
//
//	// In your Layout:
//	assets.Stylesheet("styles.css") // <link rel="stylesheet" href="/static/styles.css?v=a1b2c3d4e5f6">
type Asset struct {
	prefix   string
	fs       fs.FS
	precache []string
	hashes   map[string]string // path → 12-char hex hash
	handler  http.Handler
}

// NewAsset walks the filesystem, computes per-file content hashes, and
// returns a ready-to-use [Asset]. Panics if FS is nil.
func NewAsset(cfg AssetConfig) *Asset {
	if cfg.FS == nil {
		panic("poly: AssetConfig.FS is required")
	}

	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "/assets/"
	}
	if !strings.HasSuffix(prefix, "/") {
		panic("poly: AssetConfig.Prefix must end with \"/\"")
	}

	hashes := make(map[string]string)
	fs.WalkDir(cfg.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := fs.ReadFile(cfg.FS, path)
		h := sha256.Sum256(data)
		hashes[path] = hex.EncodeToString(h[:])[:12]
		return nil
	})

	return &Asset{
		prefix:   prefix,
		fs:       cfg.FS,
		precache: cfg.Precache,
		hashes:   hashes,
		handler:  http.FileServer(http.FS(cfg.FS)),
	}
}

// URL returns the hashed URL for the given asset path. The path is
// relative to the asset filesystem root.
//
//	assets.URL("styles.css") // "/static/styles.css?v=a1b2c3d4e5f6"
//
// Panics if the path does not exist in the filesystem — this catches
// typos at startup rather than serving broken links at runtime.
func (a *Asset) URL(path string) string {
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
