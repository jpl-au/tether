package tether

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/jpl-au/tether/dev"
	"github.com/klauspost/compress/gzip"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/js"
)

// enableDev turns on dev mode for the duration of a test and restores
// the default (production) state afterwards.
func enableDev(t *testing.T) {
	t.Helper()
	dev.Enable()
	t.Cleanup(dev.Reset)
}

// TestClientDistFresh is the freshness guard for the generated client
// assets. It re-runs the minification in-memory and decompresses every
// variant, so a stale or hand-edited client/dist fails loudly here
// rather than shipping wrong bytes. Run `go generate ./...` to refresh.
func TestClientDistFresh(t *testing.T) {
	srcDir := "client"
	distDir := filepath.Join("client", "dist")

	m := minify.New()
	m.AddFunc("application/javascript", js.Minify)

	sources, err := filepath.Glob(filepath.Join(srcDir, "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) == 0 {
		t.Fatal("no client sources found")
	}

	// Track which dist files a fresh generation would produce, so we can
	// catch stray leftovers afterwards.
	expected := map[string]bool{}

	for _, srcPath := range sources {
		name := filepath.Base(srcPath)
		src, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatal(err)
		}

		switch name {
		case clientIdiomorph:
			// Never re-minified: compressed variants must decompress back
			// to the original source bytes.
			expected[name+".gz"] = true
			expected[name+".br"] = true
			assertGunzip(t, distDir, name+".gz", src)
			assertUnbrotli(t, distDir, name+".br", src)

		case clientWorker:
			// Only the minified template is generated (no compressed
			// variants: the body is assembled and compressed at serve time).
			expected[name] = true
			assertEqualFile(t, distDir, name, minifyJS(t, m, src))

		default:
			min := minifyJS(t, m, src)
			expected[name] = true
			expected[name+".gz"] = true
			expected[name+".br"] = true
			assertEqualFile(t, distDir, name, min)
			assertGunzip(t, distDir, name+".gz", min)
			assertUnbrotli(t, distDir, name+".br", min)
		}
	}

	// No stale artifacts: every file in dist must be one a fresh run
	// would have written.
	distEntries, err := os.ReadDir(distDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range distEntries {
		if !expected[e.Name()] {
			t.Errorf("stale dist artifact %q: run `go generate ./...`", e.Name())
		}
	}
}

func minifyJS(t *testing.T, m *minify.M, src []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := m.Minify("application/javascript", &out, bytes.NewReader(src)); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func assertEqualFile(t *testing.T, dir, name string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("missing dist file %q: %v (run `go generate ./...`)", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("dist file %q is stale: run `go generate ./...`", name)
	}
}

func assertGunzip(t *testing.T, dir, name string, want []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("missing dist file %q: %v", name, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%q is not valid gzip: %v", name, err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("%q gunzip failed: %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%q does not decompress to the expected bytes: run `go generate ./...`", name)
	}
}

func assertUnbrotli(t *testing.T, dir, name string, want []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("missing dist file %q: %v", name, err)
	}
	got, err := io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("%q brotli decode failed: %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%q does not decompress to the expected bytes: run `go generate ./...`", name)
	}
}

// get drives a request through a freshly built handler and returns the
// recorded response.
func get(t *testing.T, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	h := (&App{}).jsHandler()
	req := httptest.NewRequest(http.MethodGet, "/"+path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServeProdBrotli(t *testing.T) {
	rec := get(t, "tether.js", "br, gzip")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
		t.Errorf("Content-Encoding = %q, want br", enc)
	}
	if ct := rec.Header().Get("Content-Type"); ct != jsContentType {
		t.Errorf("Content-Type = %q, want %q", ct, jsContentType)
	}
	if vary := rec.Header().Get("Vary"); vary != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", vary)
	}
	// Content-Length must match the compressed body actually written.
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length = %q, body = %d", cl, rec.Body.Len())
	}
	// The brotli body must decode to the minified source.
	got, err := io.ReadAll(brotli.NewReader(bytes.NewReader(rec.Body.Bytes())))
	if err != nil {
		t.Fatalf("brotli decode: %v", err)
	}
	want, _ := os.ReadFile(filepath.Join("client", "dist", "tether.js"))
	if !bytes.Equal(got, want) {
		t.Error("decoded brotli body does not match minified dist")
	}
}

func TestServeProdGzip(t *testing.T) {
	rec := get(t, "tether.js", "gzip")
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", enc)
	}
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	got, _ := io.ReadAll(zr)
	want, _ := os.ReadFile(filepath.Join("client", "dist", "tether.js"))
	if !bytes.Equal(got, want) {
		t.Error("decoded gzip body does not match minified dist")
	}
}

func TestServeProdIdentity(t *testing.T) {
	rec := get(t, "tether.js", "identity")
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty", enc)
	}
	want, _ := os.ReadFile(filepath.Join("client", "dist", "tether.js"))
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Error("identity body is not the minified dist bytes")
	}
	// Prod identity must be materially smaller than the raw source.
	raw, _ := os.ReadFile(filepath.Join("client", "tether.js"))
	if len(want) >= len(raw) {
		t.Errorf("minified (%d) not smaller than raw (%d)", len(want), len(raw))
	}
}

func TestServeNoAcceptEncodingIsIdentity(t *testing.T) {
	rec := get(t, "tether.js", "")
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty", enc)
	}
}

func TestServeGzipZeroQFallsBack(t *testing.T) {
	// br disqualified, gzip disqualified: identity.
	rec := get(t, "tether.js", "br;q=0, gzip;q=0")
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty (identity)", enc)
	}
	// br disqualified only: gzip wins.
	rec = get(t, "tether.js", "br;q=0, gzip")
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", enc)
	}
}

func TestServeIdiomorphIsByteIdentical(t *testing.T) {
	rec := get(t, clientIdiomorph, "identity")
	src, _ := os.ReadFile(filepath.Join("client", clientIdiomorph))
	if !bytes.Equal(rec.Body.Bytes(), src) {
		t.Error("idiomorph identity is not byte-identical to source")
	}
	// Brotli variant must still decode back to the exact source bytes.
	rec = get(t, clientIdiomorph, "br")
	if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("Content-Encoding = %q, want br", enc)
	}
	got, _ := io.ReadAll(brotli.NewReader(bytes.NewReader(rec.Body.Bytes())))
	if !bytes.Equal(got, src) {
		t.Error("idiomorph brotli does not decode to source")
	}
}

func TestServePushWorkerHeader(t *testing.T) {
	rec := get(t, clientPushWorker, "br")
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed = %q, want /", got)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
		t.Errorf("Content-Encoding = %q, want br", enc)
	}
}

func TestServeWorkerDynamicAndCompressed(t *testing.T) {
	rec := get(t, clientWorker, "br")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed = %q, want /", got)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
		t.Errorf("Content-Encoding = %q, want br", enc)
	}
	body, _ := io.ReadAll(brotli.NewReader(bytes.NewReader(rec.Body.Bytes())))
	// The version placeholder must be replaced with a content hash and
	// the template must have been minified for production.
	if bytes.Contains(body, []byte(`"tether-v1"`)) {
		t.Error("worker CACHE_VERSION was not injected")
	}
	// With no application assets the injected version is the first 12 hex
	// digits of sha256(clientVersion) - mirror buildWorkerJS's derivation.
	sum := sha256.Sum256([]byte(clientVersion()))
	wantVer := hex.EncodeToString(sum[:])[:12]
	if !bytes.Contains(body, []byte(`"tether-`+wantVer+`"`)) {
		t.Errorf("worker does not carry the derived version %q", wantVer)
	}
	if bytes.Contains(body, []byte("var CACHE_VERSION = ")) {
		t.Error("prod worker appears unminified")
	}
}

func TestServeUnknownIs404(t *testing.T) {
	if rec := get(t, "does-not-exist.js", "br"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	// Requests must not be able to reach into the dist tree.
	if rec := get(t, "dist/tether.js", "br"); rec.Code != http.StatusNotFound {
		t.Errorf("dist traversal status = %d, want 404", rec.Code)
	}
}

func TestDevServesReadableSource(t *testing.T) {
	enableDev(t)

	rec := get(t, "tether.js", "br, gzip")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Dev never compresses: the developer gets readable source.
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("dev Content-Encoding = %q, want empty", enc)
	}
	src, _ := os.ReadFile(filepath.Join("client", "tether.js"))
	if !bytes.Equal(rec.Body.Bytes(), src) {
		t.Error("dev did not serve the raw source bytes")
	}
	if ct := rec.Header().Get("Content-Type"); ct != jsContentType {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestDevWorkerIsUnminifiedAndInjected(t *testing.T) {
	enableDev(t)

	rec := get(t, clientWorker, "")
	body := rec.Body.Bytes()
	if !bytes.Contains(body, []byte("var CACHE_VERSION = ")) {
		t.Error("dev worker should be the readable, unminified source")
	}
	if bytes.Contains(body, []byte(`"tether-v1"`)) {
		t.Error("dev worker version was not injected")
	}
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed = %q, want /", got)
	}
}

func TestDevBlocksTraversal(t *testing.T) {
	enableDev(t)
	if rec := get(t, "dist/tether.js", ""); rec.Code != http.StatusNotFound {
		t.Errorf("dev dist traversal status = %d, want 404", rec.Code)
	}
}

func TestCacheBustHashStable(t *testing.T) {
	// The version hash used for ?v= cache-busting must be a stable,
	// non-empty 12-char hex string derived from the embedded content.
	v := clientVersion()
	if len(v) != 12 {
		t.Fatalf("clientVersion = %q, want 12 chars", v)
	}
	if v != clientVersion() {
		t.Error("clientVersion is not stable")
	}
	if strings.TrimLeft(v, "0123456789abcdef") != "" {
		t.Errorf("clientVersion %q is not lowercase hex", v)
	}
}

func TestNegotiateEncoding(t *testing.T) {
	cases := []struct {
		accept string
		want   string
	}{
		{"", ""},
		{"identity", ""},
		{"gzip", "gzip"},
		{"br", "br"},
		{"gzip, br", "br"},
		{"br;q=0.5, gzip;q=1.0", "br"}, // br still preferred when both allowed
		{"br;q=0, gzip", "gzip"},
		{"br;q=0, gzip;q=0", ""},
		{"*", "br"},
		{"*, gzip;q=0", "br"}, // gzip explicitly off, br via wildcard
		{"deflate", ""},
	}
	for _, c := range cases {
		if got := negotiateEncoding(c.accept); got != c.want {
			t.Errorf("negotiateEncoding(%q) = %q, want %q", c.accept, got, c.want)
		}
	}
}
