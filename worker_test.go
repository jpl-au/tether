package tether

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/push"
)

func TestClientWorkerHeader(t *testing.T) {
	handler := newTestHandler()

	t.Run("tether-worker.js gets Service-Worker-Allowed header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_tether/tether-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Header().Get("Service-Worker-Allowed") != "/" {
			t.Errorf("Service-Worker-Allowed = %q, want %q", w.Header().Get("Service-Worker-Allowed"), "/")
		}
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("tether-worker.js has content-hash cache version", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_tether/tether-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := w.Body.String()
		if strings.Contains(body, `"tether-v1"`) {
			t.Error("worker JS should not contain hardcoded tether-v1 version")
		}
		if !strings.Contains(body, `"tether-`) {
			t.Error("worker JS should contain a tether- prefixed cache version")
		}
	})

	t.Run("tether-push-worker.js gets Service-Worker-Allowed header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_tether/tether-push-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Header().Get("Service-Worker-Allowed") != "/" {
			t.Errorf("Service-Worker-Allowed = %q, want %q", w.Header().Get("Service-Worker-Allowed"), "/")
		}
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
			t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "text/javascript; charset=utf-8")
		}
	})

	// Cross-origin worker script GETs are no longer rejected server-side.
	// Browsers enforce same-origin for service worker registration natively.

	t.Run("tether.js does not get Service-Worker-Allowed header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_tether/tether.js", nil)
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

	handler := Stateful(App{Assets: []*Asset{assets}}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("GET", "/_tether/tether-worker.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "/static/styles.css?v=") {
		t.Error("worker JS should contain hashed precache URL for styles.css")
	}
	if !strings.Contains(body, "/static/logo.svg?v=") {
		t.Error("worker JS should contain hashed precache URL for logo.svg")
	}
	if strings.Contains(body, "PRECACHE_EXTRA=[]") {
		t.Error("worker JS should have replaced the empty PRECACHE_EXTRA placeholder")
	}
}

func TestClientNoPrecache(t *testing.T) {
	handler := newTestHandler()

	req := httptest.NewRequest("GET", "/_tether/tether-worker.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "PRECACHE_EXTRA=[]") {
		t.Error("worker JS should keep empty PRECACHE_EXTRA when no precache URLs given")
	}
}

func TestClientWorkerOriginCheck(t *testing.T) {
	handler := newTestHandler()

	t.Run("same-origin request is allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://myapp.com/_tether/tether-worker.js", nil)
		req.Header.Set("Origin", "http://myapp.com")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("no origin header is allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/_tether/tether-worker.js", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestHandlePushSubscribe(t *testing.T) {
	type result struct {
		sub     push.Subscription
		session string
	}
	ch := make(chan result, 1)

	handler := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Push: &PushConfig[counterState]{
			Sender: push.NewSender(push.Config{VAPIDPublicKey: "test-key"}),
			OnSubscribe: func(_ context.Context, sess *StatefulSession[counterState], sub push.Subscription) {
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

	// Use a real P-256 key pair so the subscription passes Validate().
	subKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate subscriber key: %v", err)
	}
	authBytes := make([]byte, 16)
	sub := push.Subscription{
		Endpoint: "https://push.example.com/v1/send/abc",
		Keys: push.SubscriptionKeys{
			P256dh: base64.RawURLEncoding.EncodeToString(subKey.PublicKey().Bytes()),
			Auth:   base64.RawURLEncoding.EncodeToString(authBytes),
		},
	}
	body, _ := json.Marshal(sub)

	req := httptest.NewRequest("POST", "/app", bytes.NewReader(body))
	req.Header.Set("Tether-Session", "test-session")
	req.Header.Set("Tether-Push-Subscribe", "true")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// OnSubscribe runs in a goroutine - wait for it via channel.
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
	handler := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("POST", "/app", strings.NewReader("{}"))
	req.Header.Set("Tether-Session", "test")
	req.Header.Set("Tether-Push-Subscribe", "true")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d when push not configured", w.Code, http.StatusNotFound)
	}
}

func TestHandlePushSubscribeMissingSession(t *testing.T) {
	handler := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Push: &PushConfig[counterState]{
			Sender:      push.NewSender(push.Config{VAPIDPublicKey: "test-key"}),
			OnSubscribe: func(context.Context, *StatefulSession[counterState], push.Subscription) {},
		},
	})

	req := httptest.NewRequest("POST", "/app", strings.NewReader("{}"))
	req.Header.Set("Tether-Push-Subscribe", "true")
	// No Tether-Session header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d for missing session", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePushSubscribeUnknownSession(t *testing.T) {
	handler := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Push: &PushConfig[counterState]{
			Sender:      push.NewSender(push.Config{VAPIDPublicKey: "test-key"}),
			OnSubscribe: func(context.Context, *StatefulSession[counterState], push.Subscription) {},
		},
	})

	req := httptest.NewRequest("POST", "/app", strings.NewReader("{}"))
	req.Header.Set("Tether-Session", "nonexistent")
	req.Header.Set("Tether-Push-Subscribe", "true")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d for unknown session", w.Code, http.StatusNotFound)
	}
}
