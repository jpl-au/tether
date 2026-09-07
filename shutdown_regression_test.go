package tether

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestShutdownSavesActiveFreezeState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)
		s := newTestSession(counterState{Count: 42}, newConnectedTransport())
		s.handler, s.freeze, s.sessionStore, s.codec = h, true, store, cborCodec[counterState]{}
		h.active[s.id] = s
		go s.run()
		synctest.Wait()
		if err := h.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		assertStoredCount(t, store, s.id, 42)
	})
}

func TestShutdownWaitsForFinalMutation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSessionFileStore(t)
		h := newThawHandler(store)
		s := newTestSession(counterState{}, newConnectedTransport())
		s.handler, s.sessionStore, s.codec = h, store, cborCodec[counterState]{}
		h.active[s.id] = s
		entered, release := make(chan struct{}), make(chan struct{})
		s.Update(func(c counterState) counterState { close(entered); <-release; c.Count = 42; return c })
		go s.run()
		<-entered
		done := make(chan error, 1)
		go func() { done <- h.Shutdown(context.Background()) }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Errorf("Shutdown returned before the mutation finished: %v", err)
		default:
		}
		second := make(chan error, 1)
		go func() { second <- h.Shutdown(context.Background()) }()
		synctest.Wait()
		select {
		case err := <-second:
			t.Errorf("concurrent Shutdown returned before cleanup finished: %v", err)
		default:
		}
		close(release)
		synctest.Wait()
		assertStoredCount(t, store, s.id, 42)
	})
}

func TestShutdownReleasesFrozenResources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, diffs := newSessionFileStore(t), newFileStore(t)
		group := NewGroup[counterState]()
		h := newThawHandler(store, func(c *StatefulConfig[counterState]) { c.DiffStore = diffs; c.Groups = []*Group[counterState]{group} })
		s := freezeSession(t, h, counterState{Count: 42}, nil)
		group.Add(s)
		bus := NewBus[int]()
		bus.Subscribe(s.Context(), func(int) {})
		if err := diffs.Save(context.Background(), s.id, []byte("old diff")); err != nil {
			t.Fatal(err)
		}
		if err := h.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if s.Context().Err() == nil {
			t.Error("frozen session context survived shutdown")
		}
		if bus.Len() != 0 || group.Len() != 0 {
			t.Errorf("resources retained: subscriptions=%d group=%d", bus.Len(), group.Len())
		}
		if data, err := diffs.Load(context.Background(), s.id); err != nil || data != nil {
			t.Errorf("obsolete diff retained: %q, %v", data, err)
		}
		assertStoredCount(t, store, s.id, 42)
		s.stop()
		synctest.Wait()
	})
}

func assertStoredCount(t *testing.T, store SessionStore, id string, want int) {
	t.Helper()
	data, err := store.Load(context.Background(), id)
	if err != nil || data == nil {
		t.Fatalf("missing recovery state: %v", err)
	}
	env, err := unmarshalEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	state, err := (cborCodec[counterState]{}).Unmarshal(env.State)
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != want {
		t.Errorf("saved Count=%d, want %d", state.Count, want)
	}
}
