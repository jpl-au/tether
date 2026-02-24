package poly

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
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

var workerCache struct {
	once sync.Once
	body []byte
}

// workerJS returns the service worker JS with the cache version set to
// a content hash of the embedded client files. The version is injected
// at serve time so the browser's service worker update check detects
// changes whenever the library is rebuilt with new client code.
func workerJS() []byte {
	workerCache.once.Do(func() {
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
		workerCache.body = bytes.Replace(raw,
			[]byte(`"poly-v1"`),
			[]byte(`"poly-`+version+`"`), 1)
	})
	return workerCache.body
}
