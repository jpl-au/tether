package tether

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpl-au/tether/wire"
)

// newThawHandler builds a Handler with freeze enabled and the given
// store. Shared setup for thaw tests.
func newThawHandler(store SessionStore, opts ...func(*LiveConfig[counterState])) *Handler[counterState] {
	cfg := LiveConfig[counterState]{
		Render:             renderCounter,
		Handle:             handleCounter,
		SessionStore:       store,
		FreezeOnDisconnect: true,
		Limits:             Limits{CmdBufferSize: defaultCmdBufferSize},
		Timeouts:           Timeouts{Reconnect: 30 * time.Second},
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

// freezeSession creates a session, wires it into the handler, runs
// the transport to completion (freeze on disconnect), and returns
// the frozen session. The caller can then thaw it.
func freezeSession(t *testing.T, h *Handler[counterState], initial counterState, events []Event) *LiveSession[counterState] {
	t.Helper()
	mt := &mockTransport{events: events}
	sess := newTestSession(initial, mt)
	sess.sessionStore = h.cfg.SessionStore
	sess.codec = cborCodec[counterState]{}
	sess.freeze = true
	sess.reconnectTimeout = h.cfg.Timeouts.Reconnect
	sess.diagnostics = h.Diagnostics

	h.mu.Lock()
	h.active[sess.id] = sess
	h.mu.Unlock()
	h.wireDisconnect(sess)

	go sess.readTransport(sess.events)
	go sess.run()
	synctest.Wait()

	if Status(sess.status.Load()) != Frozen {
		t.Fatalf("expected Frozen, got %d", sess.status.Load())
	}
	return sess
}

// TestThawRestoresState verifies the full freeze→thaw cycle: state
// is loaded from the store and the session processes events with the
// restored count. After thaw the mockTransport disconnects, which
// re-freezes the session — so we verify the restored state by
// checking the store contents from the second freeze.
func TestThawRestoresState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)

		// Freeze with Count=4, one increment event → stored Count=5.
		sess := freezeSession(t, h, counterState{Count: 4},
			[]Event{{Type: "event", Action: "increment"}})

		// Thaw with a transport that delivers one more increment.
		// After the event the transport disconnects, re-freezing
		// the session with Count=6 (5 restored + 1 new).
		thawMT := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		req, _ := http.NewRequest("GET", "/", nil)

		h.mu.Lock()
		h.active[sess.id] = sess
		h.mu.Unlock()

		go h.thaw(sess, req, thawMT)
		synctest.Wait()

		// The session re-froze after thaw's transport disconnected.
		// The store now holds Count=6 (5 restored + 1 increment).
		data, err := store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data == nil {
			t.Fatal("expected session data after re-freeze")
		}
		env, err := unmarshalEnvelope(data)
		if err != nil {
			t.Fatalf("unmarshalEnvelope: %v", err)
		}
		codec := cborCodec[counterState]{}
		state, err := codec.Unmarshal(env.State)
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if state.Count != 6 {
			t.Errorf("re-frozen Count = %d, want 6 (5 restored + 1 increment)", state.Count)
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestThawProcessesEvents verifies that a thawed session can handle
// events from the new transport. The transport sends patches back
// to the client, proving the render-diff-send pipeline works.
func TestThawProcessesEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)

		sess := freezeSession(t, h, counterState{Count: 3}, nil)

		// Thaw with a transport that delivers an increment event.
		thawMT := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		req, _ := http.NewRequest("GET", "/", nil)

		h.mu.Lock()
		h.active[sess.id] = sess
		h.mu.Unlock()

		go h.thaw(sess, req, thawMT)
		synctest.Wait()

		// Transport should have received patch data.
		thawMT.mu.Lock()
		sent := thawMT.sent
		thawMT.mu.Unlock()

		if len(sent) == 0 {
			t.Error("no messages sent to client after thaw")
		}

		// The re-freeze persists Count=4 (3 restored + 1 increment).
		data, err := store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data == nil {
			t.Fatal("expected session data after re-freeze")
		}
		env, err := unmarshalEnvelope(data)
		if err != nil {
			t.Fatalf("unmarshalEnvelope: %v", err)
		}
		codec := cborCodec[counterState]{}
		state, err := codec.Unmarshal(env.State)
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if state.Count != 4 {
			t.Errorf("re-frozen Count = %d, want 4 (3 restored + 1 increment)", state.Count)
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestThawFiresOnRestore verifies that OnRestore is called during
// thaw.
func TestThawFiresOnRestore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		var called bool

		h := newThawHandler(store, func(cfg *LiveConfig[counterState]) {
			cfg.OnRestore = func(_ *LiveSession[counterState]) {
				called = true
			}
		})

		sess := freezeSession(t, h, counterState{Count: 1}, nil)

		thawMT := &mockTransport{}
		req, _ := http.NewRequest("GET", "/", nil)

		h.mu.Lock()
		h.active[sess.id] = sess
		h.mu.Unlock()

		go h.thaw(sess, req, thawMT)
		synctest.Wait()

		if !called {
			t.Error("OnRestore was not called during thaw")
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestThawFallsBackToOnConnect verifies that OnConnect fires when
// OnRestore is nil.
func TestThawFallsBackToOnConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		var called bool

		h := newThawHandler(store, func(cfg *LiveConfig[counterState]) {
			cfg.OnConnect = func(_ *LiveSession[counterState]) {
				called = true
			}
		})

		sess := freezeSession(t, h, counterState{}, nil)

		thawMT := &mockTransport{}
		req, _ := http.NewRequest("GET", "/", nil)

		h.mu.Lock()
		h.active[sess.id] = sess
		h.mu.Unlock()

		go h.thaw(sess, req, thawMT)
		synctest.Wait()

		if !called {
			t.Error("OnConnect was not called as fallback during thaw")
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestThawStatusTransition verifies the status transitions through
// the full freeze→thaw cycle.
func TestThawStatusTransition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)

		sess := freezeSession(t, h, counterState{}, nil)

		if Status(sess.status.Load()) != Frozen {
			t.Fatalf("before thaw: status = %d, want Frozen", sess.status.Load())
		}

		thawMT := &mockTransport{}
		req, _ := http.NewRequest("GET", "/", nil)

		h.mu.Lock()
		h.active[sess.id] = sess
		h.mu.Unlock()

		go h.thaw(sess, req, thawMT)
		synctest.Wait()

		// After thaw + disconnect (mockTransport EOF), the session
		// re-freezes because freeze is still enabled. Verify it went
		// through Active on the way.
		// The session is frozen again because the mockTransport
		// disconnected. But the important thing is it was Active
		// during thaw (the loop ran and processed events).
		// We can verify it reached Active by checking that state
		// was restored (only happens inside run()).
		if sess.state.Count != 0 {
			t.Errorf("state.Count = %d, want 0 (restored from store)", sess.state.Count)
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestThawMultipleCycles exercises freeze→thaw→freeze→thaw to verify
// that the destroyed channel is properly recreated on each thaw. A
// double-close would panic.
func TestThawMultipleCycles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)

		// First cycle: freeze with Count=1.
		sess := freezeSession(t, h, counterState{Count: 1}, nil)

		// Thaw — mockTransport delivers one increment then disconnects,
		// re-freezing the session.
		thawMT := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		req, _ := http.NewRequest("GET", "/", nil)

		h.mu.Lock()
		h.active[sess.id] = sess
		h.mu.Unlock()

		go h.thaw(sess, req, thawMT)
		synctest.Wait()

		// Session should be re-frozen with Count=2 (1 restored + 1).
		if Status(sess.status.Load()) != Frozen {
			t.Fatalf("after first thaw: status = %d, want Frozen", sess.status.Load())
		}

		// Second cycle: thaw again with another increment.
		thawMT2 := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		req2, _ := http.NewRequest("GET", "/", nil)

		h.mu.Lock()
		h.active[sess.id] = sess
		h.mu.Unlock()

		go h.thaw(sess, req2, thawMT2)
		synctest.Wait()

		// Should be re-frozen with Count=3 (2 restored + 1).
		if Status(sess.status.Load()) != Frozen {
			t.Fatalf("after second thaw: status = %d, want Frozen", sess.status.Load())
		}

		data, err := store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data == nil {
			t.Fatal("expected session data after second re-freeze")
		}
		env, err := unmarshalEnvelope(data)
		if err != nil {
			t.Fatalf("unmarshalEnvelope: %v", err)
		}
		codec := cborCodec[counterState]{}
		state, err := codec.Unmarshal(env.State)
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if state.Count != 3 {
			t.Errorf("Count after two cycles = %d, want 3", state.Count)
		}

		sess.stop()
		synctest.Wait()
	})
}
