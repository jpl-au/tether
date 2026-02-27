package poly

import (
	"strings"
	"testing"
	"testing/fstest"
)

func testAssetFS() *Asset {
	return NewAsset(AssetConfig{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{color:red}")},
			"app.js":     &fstest.MapFile{Data: []byte("console.log('hello')")},
			"logo.svg":   &fstest.MapFile{Data: []byte("<svg></svg>")},
		},
		Prefix:   "/static/",
		Precache: []string{"styles.css", "logo.svg"},
	})
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
	a := NewAsset(AssetConfig{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("v1")},
		},
	})
	b := NewAsset(AssetConfig{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("v2")},
		},
	})

	if a.URL("styles.css") == b.URL("styles.css") {
		t.Error("different content should produce different hashes")
	}
}

func TestNewAssetDefaultPrefix(t *testing.T) {
	a := NewAsset(AssetConfig{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{}")},
		},
	})

	u := a.URL("styles.css")
	if !strings.HasPrefix(u, "/assets/") {
		t.Errorf("URL = %q, want /assets/ prefix when no Prefix configured", u)
	}
}

func TestNewAssetPanicsOnNilFS(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil FS")
		}
	}()

	NewAsset(AssetConfig{})
}

func TestNewAssetPanicsOnBadPrefix(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for prefix without trailing slash")
		}
	}()

	NewAsset(AssetConfig{
		FS:     fstest.MapFS{"a.css": &fstest.MapFile{Data: []byte("a")}},
		Prefix: "/static",
	})
}
