package tether

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/mode"
)

func testAssetFS() *Asset {
	return &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{color:red}")},
			"app.js":     &fstest.MapFile{Data: []byte("console.log('hello')")},
			"logo.svg":   &fstest.MapFile{Data: []byte("<svg></svg>")},
		},
		Prefix:   "/static/",
		Precache: []string{"styles.css", "logo.svg"},
	}
}

func TestAssetURL(t *testing.T) {
	a := testAssetFS()
	u := a.URL("styles.css")

	if !strings.HasPrefix(u, "/static/styles.css?v=") {
		t.Fatalf("URL = %q, want prefix /static/styles.css?v=", u)
	}

	// Hash should be 12 hex characters.
	parts := strings.SplitN(u, "?v=", 2)
	if len(parts) != 2 || len(parts[1]) != 12 {
		t.Errorf("hash portion = %q, want 12 hex characters", parts[1])
	}
}

func TestAssetURLPanicsOnMissing(t *testing.T) {
	a := testAssetFS()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for missing asset")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "not-here.css") {
			t.Errorf("panic = %v, want message mentioning not-here.css", r)
		}
	}()

	a.URL("not-here.css")
}

func TestAssetStylesheet(t *testing.T) {
	a := testAssetFS()
	n := a.Stylesheet("styles.css")
	html := string(n.Render())

	if !strings.Contains(html, "rel=\"stylesheet\"") {
		t.Errorf("expected rel=stylesheet, got:\n%s", html)
	}
	if !strings.Contains(html, "/static/styles.css?v=") {
		t.Errorf("expected hashed href, got:\n%s", html)
	}
}

func TestAssetScript(t *testing.T) {
	a := testAssetFS()
	n := a.Script("app.js")
	html := string(n.Render())

	if !strings.Contains(html, "<script") {
		t.Errorf("expected <script> tag, got:\n%s", html)
	}
	if !strings.Contains(html, "/static/app.js?v=") {
		t.Errorf("expected hashed src, got:\n%s", html)
	}
}

func TestAssetHashDeterministic(t *testing.T) {
	a := testAssetFS()
	b := testAssetFS()

	if a.URL("styles.css") != b.URL("styles.css") {
		t.Errorf("same content produced different hashes: %q vs %q",
			a.URL("styles.css"), b.URL("styles.css"))
	}
}

func TestAssetHashChangesWithContent(t *testing.T) {
	a := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("v1")},
		},
	}
	b := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("v2")},
		},
	}

	if a.URL("styles.css") == b.URL("styles.css") {
		t.Error("different content should produce different hashes")
	}
}

func TestAssetDefaultPrefix(t *testing.T) {
	a := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{}")},
		},
	}

	u := a.URL("styles.css")
	if !strings.HasPrefix(u, "/assets/") {
		t.Errorf("URL = %q, want /assets/ prefix when no Prefix configured", u)
	}
}

func TestAssetPanicsOnNilFS(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil FS")
		}
	}()

	a := &Asset{}
	a.URL("anything")
}

func TestAssetPanicsOnBadPrefix(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for prefix without trailing slash")
		}
	}()

	a := &Asset{
		FS:     fstest.MapFS{"a.css": &fstest.MapFile{Data: []byte("a")}},
		Prefix: "/static",
	}
	a.URL("a.css")
}

func TestAssetAutoMount(t *testing.T) {
	assets := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{color:red}")},
		},
		Prefix: "/static/",
	}

	handler := Live(LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Assets:       []*Asset{assets},
	})

	req := httptest.NewRequest("GET", "/static/styles.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "body{color:red}") {
		t.Error("expected CSS content in response body")
	}
}

func TestAssetAutoMountPage(t *testing.T) {
	assets := &Asset{
		FS: fstest.MapFS{
			"app.js": &fstest.MapFile{Data: []byte("console.log('hi')")},
		},
		Prefix: "/assets/",
	}

	handler := Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Render: renderCounter,
		Handle: func(_ Session, s counterState, _ Event) counterState { return s },
		Assets: []*Asset{assets},
	})

	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "console.log") {
		t.Error("expected JS content in response body")
	}
}

func TestAssetCacheHeadersProduction(t *testing.T) {
	assets := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{}")},
		},
		Prefix: "/static/",
	}

	handler := Live(LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Assets:       []*Asset{assets},
	})

	// Request with ?v= hash should get immutable cache headers.
	url := assets.URL("styles.css")
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
}

func TestAssetCacheHeadersDevMode(t *testing.T) {
	t.Cleanup(dev.Reset)
	assets := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{}")},
		},
		Prefix: "/static/",
	}

	handler := Live(LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Assets:       []*Asset{assets},
		DevMode:      true,
	})

	req := httptest.NewRequest("GET", "/static/styles.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store in DevMode", w.Header().Get("Cache-Control"))
	}
}

func TestMultipleAssets(t *testing.T) {
	css := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{}")},
		},
		Prefix: "/css/",
	}
	js := &Asset{
		FS: fstest.MapFS{
			"app.js": &fstest.MapFile{Data: []byte("alert(1)")},
		},
		Prefix: "/js/",
	}

	handler := Live(LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Assets:       []*Asset{css, js},
	})

	// Both prefixes should be served.
	for _, tc := range []struct {
		url  string
		want string
	}{
		{"/css/styles.css", "body{}"},
		{"/js/app.js", "alert(1)"},
	} {
		req := httptest.NewRequest("GET", tc.url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want %d", tc.url, w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: expected %q in body", tc.url, tc.want)
		}
	}
}

func TestAssetChangesWorkerVersion(t *testing.T) {
	workerBody := func(assets []*Asset) string {
		h := newClientHandler(assets)
		req := httptest.NewRequest("GET", "/tether-worker.js", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Body.String()
	}

	a1 := &Asset{
		FS:       fstest.MapFS{"a.css": &fstest.MapFile{Data: []byte("v1")}},
		Precache: []string{"a.css"},
	}
	a2 := &Asset{
		FS:       fstest.MapFS{"a.css": &fstest.MapFile{Data: []byte("v2")}},
		Precache: []string{"a.css"},
	}

	body1 := workerBody([]*Asset{a1})
	body2 := workerBody([]*Asset{a2})

	if body1 == body2 {
		t.Error("different asset content should produce different worker versions")
	}
}
