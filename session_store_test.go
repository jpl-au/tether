package tether

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// sessionFileStore is a filesystem-backed SessionStore for testing.
// TTL is recorded but not enforced — the tests verify it is passed
// correctly without relying on real expiry.
type sessionFileStore struct {
	dir     string
	lastTTL time.Duration
}

func newSessionFileStore(t *testing.T) *sessionFileStore {
	return &sessionFileStore{dir: t.TempDir()}
}

func (fs *sessionFileStore) path(id string) string {
	return filepath.Join(fs.dir, id+".session")
}

func (fs *sessionFileStore) Save(_ context.Context, id string, data []byte, ttl time.Duration) error {
	fs.lastTTL = ttl
	return os.WriteFile(fs.path(id), data, 0o600)
}

func (fs *sessionFileStore) Load(_ context.Context, id string) ([]byte, error) {
	data, err := os.ReadFile(fs.path(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func (fs *sessionFileStore) Delete(_ context.Context, id string) error {
	err := os.Remove(fs.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// --- Interface contract tests ---

func TestSessionStoreRoundTrip(t *testing.T) {
	store := newSessionFileStore(t)
	ctx := context.Background()
	payload := []byte("session-state-bytes")

	if err := store.Save(ctx, "sess-1", payload, 30*time.Second); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("Load = %q, want %q", got, payload)
	}
}

func TestSessionStoreDeleteRemovesData(t *testing.T) {
	store := newSessionFileStore(t)
	ctx := context.Background()

	if err := store.Save(ctx, "sess-2", []byte("data"), time.Minute); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(ctx, "sess-2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Load(ctx, "sess-2")
	if err != nil {
		t.Fatalf("Load after Delete: %v", err)
	}
	if got != nil {
		t.Errorf("Load after Delete = %q, want nil", got)
	}
}

func TestSessionStoreLoadMissingReturnsNil(t *testing.T) {
	store := newSessionFileStore(t)

	got, err := store.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if got != nil {
		t.Errorf("Load missing = %q, want nil", got)
	}
}

func TestSessionStoreDeleteMissingIsNoOp(t *testing.T) {
	store := newSessionFileStore(t)

	if err := store.Delete(context.Background(), "nonexistent"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
}

func TestSessionStoreTTLPassedThrough(t *testing.T) {
	store := newSessionFileStore(t)
	ctx := context.Background()
	ttl := 45 * time.Second

	if err := store.Save(ctx, "sess-3", []byte("data"), ttl); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.lastTTL != ttl {
		t.Errorf("TTL = %v, want %v", store.lastTTL, ttl)
	}
}

// --- Codec tests ---

func TestCBORCodecRoundTrip(t *testing.T) {
	codec := cborCodec[counterState]{}

	original := counterState{Count: 42}
	data, err := codec.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	restored, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if restored.Count != original.Count {
		t.Errorf("Count = %d, want %d", restored.Count, original.Count)
	}
}

// TestCBORCodecTolerateMissingFields verifies that CBOR handles
// schema evolution gracefully — a struct with fewer fields than the
// encoded data decodes without error, ignoring unknown fields.
func TestCBORCodecTolerateMissingFields(t *testing.T) {
	type v2State struct {
		Count int
		Label string
	}

	// Encode a v2 struct with an extra field.
	full := cborCodec[v2State]{}
	data, err := full.Marshal(v2State{Count: 10, Label: "hello"})
	if err != nil {
		t.Fatalf("Marshal v2: %v", err)
	}

	// Decode into v1 (counterState) which lacks Label.
	slim := cborCodec[counterState]{}
	got, err := slim.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal v1: %v", err)
	}
	if got.Count != 10 {
		t.Errorf("Count = %d, want 10", got.Count)
	}
}

// --- Envelope tests ---

func TestEnvelopeRoundTrip(t *testing.T) {
	env := sessionEnvelope{
		State:     []byte("encoded-state"),
		Endpoint:  "/dashboard",
		URL:       "/dashboard?tab=stats",
		Title:     "Dashboard",
		UserAgent: "Mozilla/5.0",
	}

	data, err := marshalEnvelope(env)
	if err != nil {
		t.Fatalf("marshalEnvelope: %v", err)
	}

	got, err := unmarshalEnvelope(data)
	if err != nil {
		t.Fatalf("unmarshalEnvelope: %v", err)
	}

	if !bytes.Equal(got.State, env.State) {
		t.Errorf("State = %q, want %q", got.State, env.State)
	}
	if got.Endpoint != env.Endpoint {
		t.Errorf("Endpoint = %q, want %q", got.Endpoint, env.Endpoint)
	}
	if got.URL != env.URL {
		t.Errorf("URL = %q, want %q", got.URL, env.URL)
	}
	if got.Title != env.Title {
		t.Errorf("Title = %q, want %q", got.Title, env.Title)
	}
	if got.UserAgent != env.UserAgent {
		t.Errorf("UserAgent = %q, want %q", got.UserAgent, env.UserAgent)
	}
}

// --- Lifecycle integration tests ---

// TestSessionStoreDisconnectPersists verifies that onTransportClose
// saves session state to the SessionStore.
func TestSessionStoreDisconnectPersists(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		mt := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// After disconnect, the store should hold the session envelope.
		data, err := store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data == nil {
			t.Fatal("expected session data after disconnect, got nil")
		}

		// Verify the envelope contains the updated state (Count=1
		// after the increment event).
		env, err := unmarshalEnvelope(data)
		if err != nil {
			t.Fatalf("unmarshalEnvelope: %v", err)
		}
		codec := cborCodec[counterState]{}
		state, err := codec.Unmarshal(env.State)
		if err != nil {
			t.Fatalf("Unmarshal state: %v", err)
		}
		if state.Count != 1 {
			t.Errorf("restored Count = %d, want 1", state.Count)
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestSessionStoreDisconnectTTLMatchesReconnect verifies that the
// TTL passed to Save on disconnect matches the reconnect timeout.
func TestSessionStoreDisconnectTTLMatchesReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		mt := &mockTransport{}
		sess := newTestSession(counterState{}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}
		sess.reconnectTimeout = 45 * time.Second

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		if store.lastTTL != 45*time.Second {
			t.Errorf("TTL = %v, want 45s", store.lastTTL)
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestSessionStoreShutdownPersists verifies that Shutdown saves
// session state after the command loop exits (no data race) and
// uses ShutdownGrace as the TTL.
func TestSessionStoreShutdownPersists(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 7}, ct)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		grace := 15 * time.Second
		h := &Handler[counterState]{
			cfg: LiveConfig[counterState]{
				SessionStore: store,
				Timeouts:     Timeouts{ShutdownGrace: grace},
			},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       map[string]*LiveSession[counterState]{sess.id: sess},
			disconnected: make(map[string]*LiveSession[counterState]),
			done:         make(chan struct{}),
		}
		h.Diagnostics = NewBus[Diagnostic]()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := h.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		synctest.Wait()

		// Verify the store holds the session data.
		data, err := store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data == nil {
			t.Fatal("expected session data after shutdown, got nil")
		}

		// Verify the persisted state matches.
		env, err := unmarshalEnvelope(data)
		if err != nil {
			t.Fatalf("unmarshalEnvelope: %v", err)
		}
		codec := cborCodec[counterState]{}
		state, err := codec.Unmarshal(env.State)
		if err != nil {
			t.Fatalf("Unmarshal state: %v", err)
		}
		if state.Count != 7 {
			t.Errorf("restored Count = %d, want 7", state.Count)
		}

		// Verify TTL matches ShutdownGrace.
		if store.lastTTL != grace {
			t.Errorf("TTL = %v, want %v", store.lastTTL, grace)
		}
	})
}
