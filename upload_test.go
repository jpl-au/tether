package tether

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jpl-au/fluent-tether/mode"
)

func TestHandleUploadNotConfigured(t *testing.T) {
	handler := newTestHandler()

	req := httptest.NewRequest("POST", "/app", nil)
	req.Header.Set("X-Tether-Upload", "avatar")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d when upload not configured", w.Code, http.StatusNotFound)
	}
}

func TestHandleUploadMissingSession(t *testing.T) {
	handler := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Upload: &UploadConfig[counterState]{
			Handle: func(*Session[counterState], Upload) error { return nil },
		},
	})

	req := httptest.NewRequest("POST", "/app", nil)
	req.Header.Set("X-Tether-Upload", "avatar")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d for missing session", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUploadUnknownSession(t *testing.T) {
	handler := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Upload: &UploadConfig[counterState]{
			Handle: func(*Session[counterState], Upload) error { return nil },
		},
	})

	req := httptest.NewRequest("POST", "/app", nil)
	req.Header.Set("X-Tether-Upload", "avatar")
	req.Header.Set("X-Tether-Session", "nonexistent")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d for unknown session", w.Code, http.StatusNotFound)
	}
}

func TestHandleUploadSuccess(t *testing.T) {
	var received Upload
	done := make(chan struct{})

	handler := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Upload: &UploadConfig[counterState]{
			Handle: func(_ *Session[counterState], u Upload) error {
				received = u
				close(done)
				return nil
			},
		},
	})

	mt := &mockTransport{}
	sess := newTestSession(counterState{}, mt)
	sess.id = "upload-session"
	handler.mu.Lock()
	handler.active["upload-session"] = sess
	handler.mu.Unlock()

	body, contentType := createMultipartBody(t, "file", "photo.jpg", "image/jpeg", []byte("fake image data"))

	req := httptest.NewRequest("POST", "/app", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Tether-Upload", "avatar")
	req.Header.Set("X-Tether-Session", "upload-session")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upload callback not called within timeout")
	}

	if received.Action != "avatar" {
		t.Errorf("action = %q, want %q", received.Action, "avatar")
	}
	if received.Name != "photo.jpg" {
		t.Errorf("name = %q, want %q", received.Name, "photo.jpg")
	}
	if received.Size != int64(len("fake image data")) {
		t.Errorf("size = %d, want %d", received.Size, len("fake image data"))
	}
}

func TestHandleUploadMIMEReject(t *testing.T) {
	handler := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Upload: &UploadConfig[counterState]{
			Handle: func(*Session[counterState], Upload) error { return nil },
			Accept: []string{"image/*"},
		},
	})

	mt := &mockTransport{}
	sess := newTestSession(counterState{}, mt)
	sess.id = "upload-session"
	handler.mu.Lock()
	handler.active["upload-session"] = sess
	handler.mu.Unlock()

	body, contentType := createMultipartBody(t, "file", "malware.exe", "application/octet-stream", []byte("bad"))

	req := httptest.NewRequest("POST", "/app", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Tether-Upload", "avatar")
	req.Header.Set("X-Tether-Session", "upload-session")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want %d for rejected MIME type", w.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandleUploadMIMEAcceptWildcard(t *testing.T) {
	done := make(chan struct{})

	handler := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Upload: &UploadConfig[counterState]{
			Handle: func(*Session[counterState], Upload) error {
				close(done)
				return nil
			},
			Accept: []string{"image/*"},
		},
	})

	mt := &mockTransport{}
	sess := newTestSession(counterState{}, mt)
	sess.id = "upload-session"
	handler.mu.Lock()
	handler.active["upload-session"] = sess
	handler.mu.Unlock()

	body, contentType := createMultipartBody(t, "file", "photo.png", "image/png", []byte("png data"))

	req := httptest.NewRequest("POST", "/app", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Tether-Upload", "avatar")
	req.Header.Set("X-Tether-Session", "upload-session")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upload callback not called within timeout")
	}
}

func TestMimeAllowed(t *testing.T) {
	tests := []struct {
		contentType string
		accept      []string
		want        bool
	}{
		{"image/png", nil, true},
		{"image/png", []string{"image/*"}, true},
		{"image/jpeg", []string{"image/*"}, true},
		{"application/pdf", []string{"image/*"}, false},
		{"application/pdf", []string{"application/pdf"}, true},
		{"image/png", []string{"image/*", "application/pdf"}, true},
		{"application/pdf", []string{"image/*", "application/pdf"}, true},
		{"text/plain", []string{"image/*", "application/pdf"}, false},
		{"", []string{"image/*"}, false},
	}

	for _, tt := range tests {
		got := mimeAllowed(tt.contentType, tt.accept)
		if got != tt.want {
			t.Errorf("mimeAllowed(%q, %v) = %v, want %v", tt.contentType, tt.accept, got, tt.want)
		}
	}
}

func TestExtensionScriptIncluded(t *testing.T) {
	body := &tetherBody{
		html:     []byte(`<input type="file" data-tether-upload="avatar">`),
		endpoint: "/app",
		session:  "abc",
	}
	var buf bytes.Buffer
	body.RenderBuilder(&buf)
	html := buf.String()

	if !strings.Contains(html, "fluent-tether-upload.js") {
		t.Error("expected fluent-tether-upload.js script tag when data-tether-upload is present")
	}
}

func TestExtensionScriptOmitted(t *testing.T) {
	body := &tetherBody{
		html:     []byte(`<button>No uploads here</button>`),
		endpoint: "/app",
		session:  "abc",
	}
	var buf bytes.Buffer
	body.RenderBuilder(&buf)
	html := buf.String()

	if strings.Contains(html, "fluent-tether-upload.js") {
		t.Error("fluent-tether-upload.js should not appear when no upload elements exist")
	}
}

// createMultipartBody builds a multipart form body with a single file part.
func createMultipartBody(t *testing.T, field, filename, contentType string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="` + field + `"; filename="` + filename + `"`}
	h["Content-Type"] = []string{contentType}

	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	w.Close()

	return &buf, w.FormDataContentType()
}
