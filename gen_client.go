//go:build ignore

// Command gen_client minifies and precompresses tether's browser-side
// JS runtime into client/dist/. It is run via `go generate ./...` and
// its output is committed to the repository so production builds embed
// ready-to-serve bytes without a Node/npm toolchain.
//
// Three rules, one per kind of file (see the switch in run):
//
//   - idiomorph.min.js is already minified and is sha256-verified
//     against the official npm dist by `fluentctl vendor`. It must stay
//     byte-identical on disk, so it is NEVER re-minified - only its
//     gzip and brotli variants are written.
//
//   - tether-worker.js is the service-worker template. Its CACHE_VERSION
//     and precache list are injected at serve time, so the wire bytes
//     differ per application and cannot be precompressed here. Only the
//     minified template is written; the runtime compresses the final
//     body once.
//
//   - every other file is first-party source: minified, then gzip and
//     brotli variants written alongside.
//
// The freshness of this output is verified by TestClientDistFresh, which
// re-runs the minification in-memory and decompresses each variant. A
// stale dist therefore fails the test suite loudly.
package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/js"
)

// idiomorphName is the one file we must never rewrite; workerName is the
// one file whose wire bytes are assembled at serve time.
const (
	idiomorphName = "idiomorph.min.js"
	workerName    = "tether-worker.js"

	// jsMIME is the media type the minifier keys its JS handler on. It
	// is not the wire Content-Type (see clientassets.go for that).
	jsMIME = "application/javascript"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("gen_client: %v", err)
	}
}

func run() error {
	srcDir := "client"
	distDir := filepath.Join(srcDir, "dist")

	// Start from an empty dist so a removed source file cannot leave a
	// stale artifact behind.
	if err := os.RemoveAll(distDir); err != nil {
		return err
	}
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}

	names, err := sourceNames(srcDir)
	if err != nil {
		return err
	}

	m := minify.New()
	m.AddFunc(jsMIME, js.Minify)

	for _, name := range names {
		src, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			return err
		}

		switch name {
		case idiomorphName:
			// Byte-identical on disk: compress the original, no plain copy.
			if err := writeCompressed(distDir, name, src); err != nil {
				return err
			}

		case workerName:
			// Dynamic body: write only the minified template.
			min, err := minifyJS(m, src)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(distDir, name), min, 0o644); err != nil {
				return err
			}

		default:
			min, err := minifyJS(m, src)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(distDir, name), min, 0o644); err != nil {
				return err
			}
			if err := writeCompressed(distDir, name, min); err != nil {
				return err
			}
		}
	}

	log.Printf("gen_client: wrote %d minified/compressed assets to %s", len(names), distDir)
	return nil
}

// sourceNames lists the *.js files directly under client/ (the dist
// subdirectory is skipped) in a stable order.
func sourceNames(srcDir string) ([]string, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".js" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func minifyJS(m *minify.M, src []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := m.Minify(jsMIME, &out, bytes.NewReader(src)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// writeCompressed writes name.gz and name.br next to the (already
// written or original) identity bytes, at maximum compression - this
// runs once at generate time so the CPU cost is paid by developers, not
// by every process at startup.
func writeCompressed(distDir, name string, body []byte) error {
	var gz bytes.Buffer
	gw, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := gw.Write(body); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(distDir, name+".gz"), gz.Bytes(), 0o644); err != nil {
		return err
	}

	var br bytes.Buffer
	bw := brotli.NewWriterLevel(&br, brotli.BestCompression)
	if _, err := bw.Write(body); err != nil {
		return err
	}
	if err := bw.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(distDir, name+".br"), br.Bytes(), 0o644)
}
