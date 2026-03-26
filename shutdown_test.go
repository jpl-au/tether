package tether

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestShutdownClosesActiveSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()

		h := &Handler[counterState]{
			app:          App{},
			cfg:          StatefulConfig[counterState]{},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       map[string]*StatefulSession[counterState]{"test": sess},
			disconnected: make(map[string]*StatefulSession[counterState]),
			done:         make(chan struct{}),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := h.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
		synctest.Wait()

		ct.mu.Lock()
		closed := ct.closed
		ct.mu.Unlock()

		if !closed {
			t.Error("expected transport to be closed after shutdown")
		}
	})
}

func TestShutdownSkipsFrozenSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)

		// Create a session, freeze it so state is persisted and
		// then zeroed in memory.
		mt := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.sessionStore = store
		sess.codec = cborCodec[counterState]{}
		sess.freeze = true
		sess.reconnectTimeout = 30 * time.Second

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		if Status(sess.status.Load()) != Frozen {
			t.Fatalf("expected Frozen, got %d", sess.status.Load())
		}

		// The store should hold Count=1 (from the increment event).
		data, err := store.Load(context.Background(), sess.id)
		if err != nil || data == nil {
			t.Fatalf("expected session data in store after freeze")
		}

		// Now run Shutdown with the frozen session in the disconnected pool.
		h := &Handler[counterState]{
			app: App{},
			cfg: StatefulConfig[counterState]{
				SessionStore: store,
			},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       make(map[string]*StatefulSession[counterState]),
			disconnected: map[string]*StatefulSession[counterState]{sess.id: sess},
			done:         make(chan struct{}),
		}
		h.Diagnostics = NewBus[Diagnostic]()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := h.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
		synctest.Wait()

		// The store should still hold the original Count=1, not
		// a zeroed state. Before the fix, Shutdown would overwrite
		// the valid snapshot with empty data.
		data, err = store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load after shutdown: %v", err)
		}
		if data == nil {
			// Frozen session's store entry should not be deleted
			// by shutdown - the client may still reconnect.
			t.Fatal("store entry should survive shutdown for frozen sessions")
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
		if state.Count != 1 {
			t.Errorf("persisted Count = %d after shutdown, want 1 (original freeze snapshot)", state.Count)
		}
	})
}

func TestShutdownStopsPendingCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &Handler[counterState]{
			app: App{},
			cfg: StatefulConfig[counterState]{Timeouts: Timeouts{
				Pending:      30 * time.Second,
				PendingCheck: defaultPendingCheckInterval,
			}},
			pending:      make(map[string]*pendingSession[counterState]),
			active:       make(map[string]*StatefulSession[counterState]),
			disconnected: make(map[string]*StatefulSession[counterState]),
			done:         make(chan struct{}),
		}

		cleanupDone := false
		go func() {
			h.reapPending()
			cleanupDone = true
		}()

		close(h.done)
		synctest.Wait()

		if !cleanupDone {
			t.Fatal("pending cleanup goroutine did not exit after done channel closed")
		}
	})
}
