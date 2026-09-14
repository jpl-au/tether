package tether

import "testing"

func TestHealthEmpty(t *testing.T) {
	handler := &Handler[counterState]{}

	h := handler.Health()
	if h.Pending != 0 || h.Active != 0 || h.Disconnected != 0 {
		t.Errorf("expected all zeros, got pending=%d active=%d disconnected=%d",
			h.Pending, h.Active, h.Disconnected)
	}
}

func TestHealthCountsPending(t *testing.T) {
	handler := &Handler[counterState]{
		pending: map[string]*pendingSession[counterState]{"a": {}},
	}

	h := handler.Health()
	if h.Pending != 1 {
		t.Errorf("pending = %d, want 1", h.Pending)
	}
}

func TestHealthCountsActive(t *testing.T) {
	handler := &Handler[counterState]{
		active: map[string]*StatefulSession[counterState]{"a": {}, "b": {}},
	}

	h := handler.Health()
	if h.Active != 2 {
		t.Errorf("active = %d, want 2", h.Active)
	}
}

func TestHealthCountsDisconnected(t *testing.T) {
	handler := &Handler[counterState]{
		disconnected: map[string]*StatefulSession[counterState]{"a": {}},
	}

	h := handler.Health()
	if h.Disconnected != 1 {
		t.Errorf("disconnected = %d, want 1", h.Disconnected)
	}
}
