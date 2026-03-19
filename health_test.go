package tether

import (
	"net/http"
	"testing"

	"github.com/jpl-au/tether/mode"
)

func TestHealthEmpty(t *testing.T) {
	handler := Live(App{}, LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	h := handler.Health()
	if h.Pending != 0 || h.Active != 0 || h.Disconnected != 0 {
		t.Errorf("expected all zeros, got pending=%d active=%d disconnected=%d",
			h.Pending, h.Active, h.Disconnected)
	}
}

func TestHealthCountsPending(t *testing.T) {
	handler := Live(App{}, LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	// Inject a pending session directly.
	handler.mu.Lock()
	handler.pending["a"] = &pendingSession[counterState]{}
	handler.mu.Unlock()

	h := handler.Health()
	if h.Pending != 1 {
		t.Errorf("pending = %d, want 1", h.Pending)
	}
}

func TestHealthCountsActive(t *testing.T) {
	handler := Live(App{}, LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	handler.mu.Lock()
	handler.active["a"] = &LiveSession[counterState]{}
	handler.active["b"] = &LiveSession[counterState]{}
	handler.mu.Unlock()

	h := handler.Health()
	if h.Active != 2 {
		t.Errorf("active = %d, want 2", h.Active)
	}
}

func TestHealthCountsDisconnected(t *testing.T) {
	handler := Live(App{}, LiveConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	handler.mu.Lock()
	handler.disconnected["a"] = &LiveSession[counterState]{}
	handler.mu.Unlock()

	h := handler.Health()
	if h.Disconnected != 1 {
		t.Errorf("disconnected = %d, want 1", h.Disconnected)
	}
}
