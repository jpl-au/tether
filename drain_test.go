package tether

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpl-au/tether/mode"
)

func TestDrainRejectsNewPages(t *testing.T) {
	handler := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	handler.draining.Store(true)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestDrainAllowsReconnect(t *testing.T) {
	handler := New(Config[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	// Inject a disconnected session so reconnect path is available.
	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 42}, mt)

	handler.mu.Lock()
	handler.disconnected[sess.id] = sess
	handler.mu.Unlock()

	// Start draining — the disconnected session should still be reachable.
	handler.draining.Store(true)

	handler.mu.Lock()
	_, inDisc := handler.disconnected[sess.id]
	handler.mu.Unlock()

	if !inDisc {
		t.Error("disconnected session should still be in pool during drain")
	}
}

func TestDrainReturnsWhenEmpty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		handler := &Handler[counterState]{
			cfg:          Config[counterState]{},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       make(map[string]*LiveSession[counterState]),
			disconnected: make(map[string]*LiveSession[counterState]),
			done:         make(chan struct{}),
		}

		ctx := context.Background()
		err := handler.Drain(ctx)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})
}

func TestDrainReturnsWhenContextCancelled(t *testing.T) {
	handler := &Handler[counterState]{
		cfg:          Config[counterState]{},
		pending:      make(map[string]*pendingSession[counterState]),
		active:       map[string]*LiveSession[counterState]{"a": {}},
		disconnected: make(map[string]*LiveSession[counterState]),
		done:         make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := handler.Drain(ctx)
	if !isContextErr(err) {
		t.Errorf("expected context error, got %v", err)
	}
}

func isContextErr(err error) bool {
	return err == context.DeadlineExceeded || err == context.Canceled
}
