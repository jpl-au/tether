package tether

import (
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

	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), "no-store")
	}
}

func TestStatefulNoCacheControlInProduction(t *testing.T) {
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

	if w.Header().Get("Cache-Control") != "" {
		t.Errorf("Cache-Control = %q, want empty when DevMode is false", w.Header().Get("Cache-Control"))
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
