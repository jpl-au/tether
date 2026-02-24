package poly

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServeClientWorkerHeader(t *testing.T) {
	handler := ServeClient()

	t.Run("poly-worker.js gets Service-Worker-Allowed header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/poly-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Header().Get("Service-Worker-Allowed") != "/" {
			t.Errorf("Service-Worker-Allowed = %q, want %q", w.Header().Get("Service-Worker-Allowed"), "/")
		}
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("fluent-poly.js does not get Service-Worker-Allowed header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/fluent-poly.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Header().Get("Service-Worker-Allowed") != "" {
			t.Errorf("Service-Worker-Allowed should be empty for non-worker files, got %q", w.Header().Get("Service-Worker-Allowed"))
		}
	})
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
	var received PushSubscription
	var receivedSession string

	handler := New(Config[counterState]{
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Push: &PushConfig[counterState]{
			VAPIDPublicKey: "test-key",
			OnSubscribe: func(sess *Session[counterState], sub PushSubscription) {
				received = sub
				receivedSession = sess.ID()
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

	sub := PushSubscription{
		Endpoint: "https://push.example.com/v1/send/abc",
		Keys: PushSubscriptionKeys{
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

	// OnSubscribe runs in a goroutine, give it a moment.
	for i := 0; i < 100; i++ {
		if receivedSession != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if received.Endpoint != sub.Endpoint {
		t.Errorf("endpoint = %q, want %q", received.Endpoint, sub.Endpoint)
	}
	if received.Keys.P256dh != sub.Keys.P256dh {
		t.Errorf("p256dh = %q, want %q", received.Keys.P256dh, sub.Keys.P256dh)
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
			VAPIDPublicKey: "test-key",
			OnSubscribe:    func(*Session[counterState], PushSubscription) {},
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
			VAPIDPublicKey: "test-key",
			OnSubscribe:    func(*Session[counterState], PushSubscription) {},
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

// stubUpgrade is a no-op upgrade function for tests that don't need
// a real transport connection.
func stubUpgrade(w http.ResponseWriter, r *http.Request) (Transport, error) {
	return &mockTransport{}, nil
}
