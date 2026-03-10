package tether

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/tether/mode"
)

func TestSessionBindingRejectsReattachWithMismatchedUA(t *testing.T) {
	h := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	// Inject a disconnected session with a known User-Agent.
	sess := newTestSession(counterState{Count: 42}, &mockTransport{})
	sess.userAgent = "Mozilla/5.0 OriginalBrowser"

	h.mu.Lock()
	h.disconnected[sess.id] = sess
	h.mu.Unlock()

	// Subscribe to diagnostics to verify the event is emitted.
	ctx := t.Context()
	var got Diagnostic
	h.Diagnostics.Subscribe(ctx, func(d Diagnostic) { got = d })

	// Attempt to reconnect with a different User-Agent.
	req := httptest.NewRequest("GET", "/?session="+sess.id, nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("User-Agent", "curl/7.68")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Session should remain in disconnected — the legitimate client
	// can still reconnect with the correct UA.
	h.mu.Lock()
	_, inDisc := h.disconnected[sess.id]
	_, inActive := h.active[sess.id]
	h.mu.Unlock()

	if !inDisc {
		t.Error("session should remain in disconnected pool after binding failure")
	}
	if inActive {
		t.Error("session should not be in active pool after binding failure")
	}
	if got.Kind != SessionBindingFailed {
		t.Errorf("expected SessionBindingFailed diagnostic, got %q", got.Kind)
	}
}

func TestSessionBindingRejectsPendingClaimWithMismatchedUA(t *testing.T) {
	h := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	// Inject a pending session with a known User-Agent.
	h.mu.Lock()
	h.pending["test-pending"] = &pendingSession[counterState]{
		state:     counterState{Count: 10},
		differ:    jit.NewDiffer(),
		createdAt: time.Now(),
		userAgent: "Mozilla/5.0 OriginalBrowser",
	}
	h.mu.Unlock()

	ctx := t.Context()
	var got Diagnostic
	h.Diagnostics.Subscribe(ctx, func(d Diagnostic) { got = d })

	// Attempt to claim with a different User-Agent.
	req := httptest.NewRequest("GET", "/?session=test-pending", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("User-Agent", "curl/7.68")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Pending session should be deleted to prevent further attempts.
	h.mu.Lock()
	_, inPending := h.pending["test-pending"]
	h.mu.Unlock()

	if inPending {
		t.Error("pending session should be deleted after binding failure")
	}
	if got.Kind != SessionBindingFailed {
		t.Errorf("expected SessionBindingFailed diagnostic, got %q", got.Kind)
	}
}

func TestSessionBindingDisabledAllowsMismatchedUA(t *testing.T) {
	connected := make(chan struct{}, 1)
	h := New(Config[counterState]{
		Mode:    mode.WebSocket,
		Upgrade: stubUpgrade,
		Security: Security{
			DisableSessionBinding: true,
		},
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		OnConnect: func(sess *LiveSession[counterState]) {
			connected <- struct{}{}
		},
	})

	// Inject a pending session with a known User-Agent.
	h.mu.Lock()
	h.pending["test-disabled"] = &pendingSession[counterState]{
		state:     counterState{Count: 5},
		differ:    jit.NewDiffer(),
		createdAt: time.Now(),
		userAgent: "Mozilla/5.0 OriginalBrowser",
	}
	h.mu.Unlock()

	// Claim with a different User-Agent — should succeed because
	// binding is disabled.
	req := httptest.NewRequest("GET", "/?session=test-disabled", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("User-Agent", "curl/7.68")
	w := httptest.NewRecorder()

	go h.ServeHTTP(w, req)

	// OnConnect fires only if the session was successfully claimed.
	select {
	case <-connected:
		// Success — session was created despite UA mismatch.
	case <-time.After(2 * time.Second):
		t.Fatal("expected OnConnect when binding is disabled, but it was not called")
	}

	h.Shutdown(context.Background())
}

func TestSessionBindingCapturesUAOnInitialPage(t *testing.T) {
	h := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "TestBrowser/1.0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Find the pending session and verify its User-Agent.
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.pending) != 1 {
		t.Fatalf("expected 1 pending session, got %d", len(h.pending))
	}
	for _, ps := range h.pending {
		if ps.userAgent != "TestBrowser/1.0" {
			t.Errorf("expected userAgent %q, got %q", "TestBrowser/1.0", ps.userAgent)
		}
	}
}
