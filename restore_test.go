package tether

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpl-au/tether/wire"
)

// newRestoreHandler builds a Handler with a SessionStore configured.
func newRestoreHandler(store SessionStore, opts ...func(*LiveConfig[counterState])) *Handler[counterState] {
	cfg := LiveConfig[counterState]{
		Render:       renderCounter,
		Handle:       handleCounter,
		SessionStore: store,
		Limits:       Limits{CmdBufferSize: defaultCmdBufferSize},
		Timeouts:     Timeouts{Reconnect: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	h := &Handler[counterState]{
		cfg:          cfg,
		pending:      make(map[string]*pendingSession[counterState]),
		active:       make(map[string]*LiveSession[counterState]),
		disconnected: make(map[string]*LiveSession[counterState]),
		done:         make(chan struct{}),
		encoder:      wire.JSONEncoder{},
	}
	h.Diagnostics = NewBus[Diagnostic]()
	return h
}

// saveToStore persists a counterState into the store under the given
// ID, simulating what onTransportClose does before a server restart.
func saveToStore(t *testing.T, store SessionStore, id string, state counterState) {
	t.Helper()
	codec := cborCodec[counterState]{}
	stateBytes, err := codec.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	env := sessionEnvelope{
		State:     stateBytes,
		Endpoint:  "/test",
		URL:       "/test?page=1",
		Title:     "Test Page",
		UserAgent: "TestAgent/1.0",
	}
	data, err := marshalEnvelope(env)
	if err != nil {
		t.Fatalf("marshalEnvelope: %v", err)
	}
	if err := store.Save(context.Background(), id, data, time.Minute); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// TestRestoreSessionLoadsState verifies that restoreSession loads
// state from the store, creates a working session, and cleans up
// the store entry.
func TestRestoreSessionLoadsState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newRestoreHandler(store)

		id := "restore-test-1"
		saveToStore(t, store, id, counterState{Count: 7})

		// Simulate a reconnecting client with a transport that
		// delivers one event then disconnects.
		mt := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		req, _ := http.NewRequest("GET", "/?session="+id, nil)
		req.Header.Set("User-Agent", "TestAgent/1.0")

		sess, ok := h.restoreSession(id, req, mt)
		synctest.Wait()

		if !ok {
			t.Fatal("restoreSession returned false")
		}
		if sess == nil {
			t.Fatal("restoreSession returned nil session")
		}

		// The session processed one increment: 7 + 1 = 8.
		// After disconnect the session is destroyed, so check
		// what the transport received.
		mt.mu.Lock()
		sent := mt.sent
		mt.mu.Unlock()

		if len(sent) == 0 {
			t.Error("no messages sent to client after restore")
		}

		// The initial store entry is deleted after restore, but the
		// mockTransport disconnect triggers onTransportClose which
		// saves a new entry for potential re-reconnect. Verify the
		// stored state reflects the incremented count (7 + 1 = 8).
		data, err := store.Load(context.Background(), id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data != nil {
			env, err := unmarshalEnvelope(data)
			if err != nil {
				t.Fatalf("unmarshalEnvelope: %v", err)
			}
			codec := cborCodec[counterState]{}
			state, err := codec.Unmarshal(env.State)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if state.Count != 8 {
				t.Errorf("re-persisted Count = %d, want 8", state.Count)
			}
		}
	})
}

// TestRestoreSessionFiresOnRestore verifies that OnRestore is called
// during crash recovery.
func TestRestoreSessionFiresOnRestore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		var called bool

		h := newRestoreHandler(store, func(cfg *LiveConfig[counterState]) {
			cfg.OnRestore = func(_ *LiveSession[counterState]) {
				called = true
			}
		})

		id := "restore-test-2"
		saveToStore(t, store, id, counterState{Count: 1})

		mt := &mockTransport{}
		req, _ := http.NewRequest("GET", "/?session="+id, nil)
		req.Header.Set("User-Agent", "TestAgent/1.0")

		_, ok := h.restoreSession(id, req, mt)
		synctest.Wait()

		if !ok {
			t.Fatal("restoreSession returned false")
		}
		if !called {
			t.Error("OnRestore was not called during crash recovery")
		}
	})
}

// TestRestoreSessionFallsBackToOnConnect verifies that OnConnect
// fires when OnRestore is nil during crash recovery.
func TestRestoreSessionFallsBackToOnConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		var called bool

		h := newRestoreHandler(store, func(cfg *LiveConfig[counterState]) {
			cfg.OnConnect = func(_ *LiveSession[counterState]) {
				called = true
			}
		})

		id := "restore-test-3"
		saveToStore(t, store, id, counterState{})

		mt := &mockTransport{}
		req, _ := http.NewRequest("GET", "/?session="+id, nil)
		req.Header.Set("User-Agent", "TestAgent/1.0")

		_, ok := h.restoreSession(id, req, mt)
		synctest.Wait()

		if !ok {
			t.Fatal("restoreSession returned false")
		}
		if !called {
			t.Error("OnConnect was not called as fallback during crash recovery")
		}
	})
}

// TestRestoreSessionMissingDataReturnsFalse verifies that
// restoreSession returns false when the store has no data for the ID.
func TestRestoreSessionMissingDataReturnsFalse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newRestoreHandler(store)

		mt := &mockTransport{}
		req, _ := http.NewRequest("GET", "/?session=nonexistent", nil)

		_, ok := h.restoreSession("nonexistent", req, mt)
		synctest.Wait()

		if ok {
			t.Error("restoreSession should return false for missing data")
		}
	})
}

// TestRestoreSessionBindingMismatch verifies that restoreSession
// rejects a reconnect when the User-Agent does not match.
func TestRestoreSessionBindingMismatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newRestoreHandler(store)

		id := "restore-test-binding"
		saveToStore(t, store, id, counterState{Count: 5})

		mt := &mockTransport{}
		req, _ := http.NewRequest("GET", "/?session="+id, nil)
		req.Header.Set("User-Agent", "DifferentAgent/2.0") // mismatch

		_, ok := h.restoreSession(id, req, mt)
		synctest.Wait()

		if ok {
			t.Error("restoreSession should reject mismatched User-Agent")
		}
	})
}

// TestRestoreSessionMetadata verifies that endpoint, URL, and title
// are restored from the envelope.
func TestRestoreSessionMetadata(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newRestoreHandler(store)

		id := "restore-test-meta"
		saveToStore(t, store, id, counterState{Count: 2})

		mt := &mockTransport{}
		req, _ := http.NewRequest("GET", "/?session="+id, nil)
		req.Header.Set("User-Agent", "TestAgent/1.0")

		sess, ok := h.restoreSession(id, req, mt)
		synctest.Wait()

		if !ok {
			t.Fatal("restoreSession returned false")
		}

		if sess.endpoint != "/test" {
			t.Errorf("endpoint = %q, want /test", sess.endpoint)
		}
		if sess.lastURL != "/test?page=1" {
			t.Errorf("lastURL = %q, want /test?page=1", sess.lastURL)
		}
		if sess.lastTitle != "Test Page" {
			t.Errorf("lastTitle = %q, want Test Page", sess.lastTitle)
		}
	})
}
