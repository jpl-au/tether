package poly

import (
	"embed"
	"io/fs"
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
