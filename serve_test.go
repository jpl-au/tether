package tether

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/mode"
)

func TestStatefulDevModeEnvVar(t *testing.T) {
	t.Setenv("TETHER_DEV", "1")
	t.Cleanup(dev.Reset)

	Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	if !dev.Enabled() {
		t.Error("expected dev mode to be active when TETHER_DEV is set")
	}
}

func TestStatefulDevModeBoolOverridesEnv(t *testing.T) {
	t.Setenv("TETHER_DEV", "")
	t.Cleanup(dev.Reset)

	Stateful(App{DevMode: true}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	if !dev.Enabled() {
		t.Error("expected dev mode to remain active when DevMode is true")
	}
}

func TestStatefulDevModeCacheControl(t *testing.T) {
	t.Cleanup(dev.Reset)

	handler := Stateful(App{DevMode: true}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), "private, no-store")
	}
}

// The initial page embeds the session ID (a bearer token), so it must
// carry no-store in production too - a shared cache serving one
// user's page to another would hand over the session.
func TestStatefulCacheControlInProduction(t *testing.T) {
	handler := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), "private, no-store")
	}
}

func TestStatefulDevModeInitialPageHasAttribute(t *testing.T) {
	t.Cleanup(dev.Reset)

	handler := Stateful(App{DevMode: true}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "data-tether-dev") {
		t.Error("expected data-tether-dev attribute in initial page HTML")
	}
}

func TestStatefulInitialPageHasReconnectionAttributes(t *testing.T) {
	handler := newTestHandler()

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `data-tether-retry-delay="500"`) {
		t.Error("expected default retry-delay of 500ms")
	}
	if !strings.Contains(body, `data-tether-max-retry-delay="10000"`) {
		t.Error("expected default max-retry-delay of 10000ms")
	}
	if !strings.Contains(body, `data-tether-backoff-multiplier="1.5"`) {
		t.Error("expected default backoff-multiplier of 1.5")
	}
	if !strings.Contains(body, "data-tether-jitter") {
		t.Error("expected data-tether-jitter attribute (default enabled)")
	}
}

// failingWriter satisfies http.ResponseWriter but rejects every body
// write, simulating a client that vanished mid-response.
type failingWriter struct {
	header http.Header
}

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }
func (f *failingWriter) WriteHeader(int)           {}

// TestStatefulFailedPageWriteFreesPendingSlot verifies that when the
// initial page write fails, the pending session is discarded
// immediately rather than occupying a MaxPending slot until the reaper
// collects it. A client that never received the page can never connect
// a transport for that session.
func TestStatefulFailedPageWriteFreesPendingSlot(t *testing.T) {
	t.Cleanup(dev.Reset)

	handler := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	handler.ServeHTTP(&failingWriter{}, req)

	handler.mu.Lock()
	pending := len(handler.pending)
	handler.mu.Unlock()
	if pending != 0 {
		t.Errorf("failed page write should discard the pending session, %d still pending", pending)
	}

	// A successful write must keep its pending session for the
	// transport connect.
	req = httptest.NewRequest("GET", "/app", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	handler.mu.Lock()
	pending = len(handler.pending)
	handler.mu.Unlock()
	if pending != 1 {
		t.Errorf("successful page write should keep its pending session, got %d", pending)
	}
}
