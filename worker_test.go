package poly

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jpl-au/fluent-poly/push"
)

func TestClientWorkerHeader(t *testing.T) {
	handler := newTestHandler()

	t.Run("fluent-poly-worker.js gets Service-Worker-Allowed header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_poly/fluent-poly-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Header().Get("Service-Worker-Allowed") != "/" {
			t.Errorf("Service-Worker-Allowed = %q, want %q", w.Header().Get("Service-Worker-Allowed"), "/")
		}
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("fluent-poly-worker.js has content-hash cache version", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_poly/fluent-poly-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := w.Body.String()
		if strings.Contains(body, `"poly-v1"`) {
			t.Error("worker JS should not contain hardcoded poly-v1 version")
		}
		if !strings.Contains(body, `"poly-`) {
			t.Error("worker JS should contain a poly- prefixed cache version")
		}
	})

	t.Run("fluent-poly.js does not get Service-Worker-Allowed header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_poly/fluent-poly.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Header().Get("Service-Worker-Allowed") != "" {
			t.Errorf("Service-Worker-Allowed should be empty for non-worker files, got %q", w.Header().Get("Service-Worker-Allowed"))
		}
	})
}

func TestClientPrecache(t *testing.T) {
	assets := &Asset{
		FS: fstest.MapFS{
			"styles.css": &fstest.MapFile{Data: []byte("body{}")},
			"logo.svg":   &fstest.MapFile{Data: []byte("<svg></svg>")},
		},
		Prefix:   "/static/",
		Precache: []string{"styles.css", "logo.svg"},
	}

	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Assets:       []*Asset{assets},
	})

	req := httptest.NewRequest("GET", "/_poly/fluent-poly-worker.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "/static/styles.css?v=") {
		t.Error("worker JS should contain hashed precache URL for styles.css")
	}
	if !strings.Contains(body, "/static/logo.svg?v=") {
		t.Error("worker JS should contain hashed precache URL for logo.svg")
	}
	if strings.Contains(body, "PRECACHE_EXTRA = []") {
		t.Error("worker JS should have replaced the empty PRECACHE_EXTRA placeholder")
	}
}

func TestClientNoPrecache(t *testing.T) {
	handler := newTestHandler()

	req := httptest.NewRequest("GET", "/_poly/fluent-poly-worker.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "PRECACHE_EXTRA = []") {
		t.Error("worker JS should keep empty PRECACHE_EXTRA when no precache URLs given")
	}
}

func TestClientWorkerOriginCheck(t *testing.T) {
	handler := newTestHandler()

	t.Run("cross-origin request is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://myapp.com/_poly/fluent-poly-worker.js", nil)
		req.Header.Set("Origin", "https://evil.com")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})

	t.Run("same-origin request is allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://myapp.com/_poly/fluent-poly-worker.js", nil)
		req.Header.Set("Origin", "http://myapp.com")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("no origin header is allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_poly/fluent-poly-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestPolyBodyScriptHashes(t *testing.T) {
	body := &polyBody{
		html:     []byte(`<input type="file" data-poly-upload="avatar">`),
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

	want := "/_poly/idiomorph.min.js?v=" + v
	if !strings.Contains(html, want) {
		t.Errorf("expected %q in rendered HTML", want)
	}

	want = "/_poly/fluent-poly.js?v=" + v
	if !strings.Contains(html, want) {
		t.Errorf("expected %q in rendered HTML", want)
	}

	// Extension scripts also get the hash.
	want = "/_poly/fluent-poly-upload.js?v=" + v
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

func TestPolyBodyWorkerAttribute(t *testing.T) {
	t.Run("worker true emits data-poly-worker", func(t *testing.T) {
		body := &polyBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			worker:   true,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if !strings.Contains(html, "data-poly-worker") {
			t.Error("expected data-poly-worker attribute when worker is true")
		}
	})

	t.Run("worker false omits data-poly-worker", func(t *testing.T) {
		body := &polyBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			worker:   false,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-poly-worker") {
			t.Error("data-poly-worker should not appear when worker is false")
		}
	})
}

func TestPolyBodyPushKeyAttribute(t *testing.T) {
	t.Run("push key emits data-poly-push-key", func(t *testing.T) {
		body := &polyBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			pushKey:  "BPxGS7VkOmYZ",
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if !strings.Contains(html, `data-poly-push-key="BPxGS7VkOmYZ"`) {
			t.Errorf("expected data-poly-push-key attribute, got:\n%s", html)
		}
	})

	t.Run("empty push key omits attribute", func(t *testing.T) {
		body := &polyBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			pushKey:  "",
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-poly-push-key") {
			t.Error("data-poly-push-key should not appear when pushKey is empty")
		}
	})

	t.Run("push key is HTML-escaped", func(t *testing.T) {
		body := &polyBody{
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
		if !strings.Contains(html, "data-poly-push-key=") {
			t.Error("expected data-poly-push-key attribute")
		}
	})
}

func TestHandlePushSubscribe(t *testing.T) {
	type result struct {
		sub     push.Subscription
		session string
	}
	ch := make(chan result, 1)

	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Push: &PushConfig[counterState]{
			Sender: push.NewSender(push.Config{VAPIDPublicKey: "test-key"}),
			OnSubscribe: func(sess *Session[counterState], sub push.Subscription) {
				ch <- result{sub: sub, session: sess.ID()}
			},
		},
	})

	// Create an active session so the subscribe handler can find it.
	mt := &mockTransport{}
	sess := newTestSession(counterState{}, mt)
	sess.id = "test-session"
	handler.mu.Lock()
	handler.active["test-session"] = sess
	handler.mu.Unlock()

	sub := push.Subscription{
		Endpoint: "https://push.example.com/v1/send/abc",
		Keys: push.SubscriptionKeys{
			P256dh: "subscriber-public-key",
			Auth:   "subscriber-auth-secret",
		},
	}
	body, _ := json.Marshal(sub)

	req := httptest.NewRequest("POST", "/app", bytes.NewReader(body))
	req.Header.Set("X-Poly-Session", "test-session")
	req.Header.Set("X-Poly-Push-Subscribe", "true")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// OnSubscribe runs in a goroutine — wait for it via channel.
	select {
	case got := <-ch:
		if got.sub.Endpoint != sub.Endpoint {
			t.Errorf("endpoint = %q, want %q", got.sub.Endpoint, sub.Endpoint)
		}
		if got.sub.Keys.P256dh != sub.Keys.P256dh {
			t.Errorf("p256dh = %q, want %q", got.sub.Keys.P256dh, sub.Keys.P256dh)
		}
		if got.session != "test-session" {
			t.Errorf("session = %q, want %q", got.session, "test-session")
		}
	case <-time.After(time.Second):
		t.Fatal("OnSubscribe was not called within 1 second")
	}
}

func TestHandlePushSubscribeNoPush(t *testing.T) {
	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("POST", "/app", strings.NewReader("{}"))
	req.Header.Set("X-Poly-Session", "test")
	req.Header.Set("X-Poly-Push-Subscribe", "true")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d when push not configured", w.Code, http.StatusNotFound)
	}
}

func TestHandlePushSubscribeMissingSession(t *testing.T) {
	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Push: &PushConfig[counterState]{
			Sender:      push.NewSender(push.Config{VAPIDPublicKey: "test-key"}),
			OnSubscribe: func(*Session[counterState], push.Subscription) {},
		},
	})

	req := httptest.NewRequest("POST", "/app", strings.NewReader("{}"))
	req.Header.Set("X-Poly-Push-Subscribe", "true")
	// No X-Poly-Session header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d for missing session", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePushSubscribeUnknownSession(t *testing.T) {
	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Push: &PushConfig[counterState]{
			Sender:      push.NewSender(push.Config{VAPIDPublicKey: "test-key"}),
			OnSubscribe: func(*Session[counterState], push.Subscription) {},
		},
	})

	req := httptest.NewRequest("POST", "/app", strings.NewReader("{}"))
	req.Header.Set("X-Poly-Session", "nonexistent")
	req.Header.Set("X-Poly-Push-Subscribe", "true")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d for unknown session", w.Code, http.StatusNotFound)
	}
}

func TestPolyBodyDevModeAttribute(t *testing.T) {
	t.Run("devMode true emits data-poly-dev", func(t *testing.T) {
		body := &polyBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			devMode:  true,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if !strings.Contains(html, "data-poly-dev") {
			t.Error("expected data-poly-dev attribute when devMode is true")
		}
	})

	t.Run("devMode false omits data-poly-dev", func(t *testing.T) {
		body := &polyBody{
			html:     []byte("<p>hello</p>"),
			endpoint: "/app",
			session:  "abc",
			devMode:  false,
		}
		var buf bytes.Buffer
		body.RenderBuilder(&buf)
		html := buf.String()

		if strings.Contains(html, "data-poly-dev") {
			t.Error("data-poly-dev should not appear when devMode is false")
		}
	})
}

func TestDevModeEnvVar(t *testing.T) {
	t.Setenv("POLY_DEV", "1")

	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	if !handler.cfg.DevMode {
		t.Error("expected DevMode to be true when POLY_DEV is set")
	}
}

func TestDevModeBoolOverridesEnv(t *testing.T) {
	t.Setenv("POLY_DEV", "")

	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		DevMode:      true,
	})

	if !handler.cfg.DevMode {
		t.Error("expected DevMode to remain true even without POLY_DEV")
	}
}

func TestDevModeCacheControl(t *testing.T) {
	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		DevMode:      true,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), "no-store")
	}
}

func TestDevModeNoCacheControlInProduction(t *testing.T) {
	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Cache-Control") != "" {
		t.Errorf("Cache-Control = %q, want empty when DevMode is false", w.Header().Get("Cache-Control"))
	}
}

func TestDevModeInitialPageHasAttribute(t *testing.T) {
	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		DevMode:      true,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "data-poly-dev") {
		t.Error("expected data-poly-dev attribute in initial page HTML")
	}
}

// newTestHandler creates a Handler with default test configuration.
func newTestHandler() *Handler[counterState] {
	return New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})
}

// stubUpgrade is a no-op upgrade function for tests that don't need
// a real transport connection.
func stubUpgrade(w http.ResponseWriter, r *http.Request) (Transport, error) {
	return &mockTransport{}, nil
}
