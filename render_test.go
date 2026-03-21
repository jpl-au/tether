package tether

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/mode"
)

func TestTetherBodyScriptHashes(t *testing.T) {
	body := &tetherBody{
		html:     []byte(`<input type="file" data-tether-upload="avatar">`),
		endpoint: "/app",
		session:  "abc",
	}
	var buf bytes.Buffer
	body.RenderBuilder(&buf)
	html := buf.String()

	v := clientVersion()
	if len(v) != 12 {
		t.Fatalf("clientVersion() = %q, want 12-character hex string", v)
	}

	want := "/_tether/idiomorph.min.js?v=" + v
	if !strings.Contains(html, want) {
		t.Errorf("expected %q in rendered HTML", want)
	}

	want = "/_tether/tether.js?v=" + v
	if !strings.Contains(html, want) {
		t.Errorf("expected %q in rendered HTML", want)
	}

	// Extension scripts also get the hash.
	want = "/_tether/tether-upload.js?v=" + v
	if !strings.Contains(html, want) {
		t.Errorf("expected %q in rendered HTML", want)
	}
}

func TestClientVersionDeterministic(t *testing.T) {
	a := clientVersion()
	b := clientVersion()
	if a != b {
		t.Errorf("clientVersion() not deterministic: %q != %q", a, b)
	}
}

func TestTetherBodyWorkerAttribute(t *testing.T) {
	t.Run("worker true emits data-tether-worker", func(t *testing.T) {
		body := &tetherBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			worker:   true,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if !strings.Contains(html, "data-tether-worker") {
			t.Error("expected data-tether-worker attribute when worker is true")
		}
	})

	t.Run("worker false omits data-tether-worker", func(t *testing.T) {
		body := &tetherBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			worker:   false,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-tether-worker") {
			t.Error("data-tether-worker should not appear when worker is false")
		}
	})
}

func TestTetherBodyPushKeyAttribute(t *testing.T) {
	t.Run("push key emits data-tether-push-key", func(t *testing.T) {
		body := &tetherBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			pushKey:  "BPxGS7VkOmYZ",
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if !strings.Contains(html, `data-tether-push-key="BPxGS7VkOmYZ"`) {
			t.Errorf("expected data-tether-push-key attribute, got:\n%s", html)
		}
	})

	t.Run("empty push key omits attribute", func(t *testing.T) {
		body := &tetherBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			pushKey:  "",
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-tether-push-key") {
			t.Error("data-tether-push-key should not appear when pushKey is empty")
		}
	})

	t.Run("push key is HTML-escaped", func(t *testing.T) {
		body := &tetherBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			pushKey:  `key"with<special>&chars`,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, `key"with<special>&chars`) {
			t.Error("push key should be HTML-escaped")
		}
		if !strings.Contains(html, "data-tether-push-key=") {
			t.Error("expected data-tether-push-key attribute")
		}
	})
}

func TestTetherBodyDevModeAttribute(t *testing.T) {
	t.Run("devMode true emits data-tether-dev", func(t *testing.T) {
		dev.Enable()
		t.Cleanup(dev.Reset)

		body := &tetherBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if !strings.Contains(html, "data-tether-dev") {
			t.Error("expected data-tether-dev attribute when devMode is true")
		}
	})

	t.Run("devMode false omits data-tether-dev", func(t *testing.T) {
		dev.Reset()

		body := &tetherBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-tether-dev") {
			t.Error("data-tether-dev should not appear when devMode is false")
		}
	})
}

func TestTetherBodyBackoffAttributes(t *testing.T) {
	t.Run("WebSocket transport emits backoff and jitter attributes", func(t *testing.T) {
		body := &tetherBody{
			html:              []byte("<p>hello</p>"),
			endpoint:          "/app",
			session:           "abc",
			transport:         mode.WebSocket,
			retryDelay:        500,
			maxRetryDelay:     10000,
			backoffMultiplier: 1.5,
			jitter:            true,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if !strings.Contains(html, `data-tether-backoff-multiplier="1.5"`) {
			t.Error("expected data-tether-backoff-multiplier attribute")
		}
		if !strings.Contains(html, "data-tether-jitter") {
			t.Error("expected data-tether-jitter attribute")
		}
	})

	t.Run("jitter false omits data-tether-jitter", func(t *testing.T) {
		body := &tetherBody{
			html:              []byte("<p>hello</p>"),
			endpoint:          "/app",
			session:           "abc",
			transport:         mode.WebSocket,
			retryDelay:        500,
			maxRetryDelay:     10000,
			backoffMultiplier: 1.5,
			jitter:            false,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-tether-jitter") {
			t.Error("data-tether-jitter should not appear when jitter is false")
		}
	})

	t.Run("fetch transport omits all reconnection attributes", func(t *testing.T) {
		body := &tetherBody{
			html:              []byte("<p>hello</p>"),
			endpoint:          "/app",
			transport:         mode.HTTP,
			backoffMultiplier: 1.5,
			jitter:            true,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-tether-backoff-multiplier") {
			t.Error("fetch mode should not have backoff-multiplier attribute")
		}
		if strings.Contains(html, "data-tether-jitter") {
			t.Error("fetch mode should not have jitter attribute")
		}
	})
}
