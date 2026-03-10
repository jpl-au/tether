package tether

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
)

// fileStore is a filesystem-backed DiffStore for testing. Each session
// ID maps to a file in dir. This validates the interface contract
// against real I/O rather than in-memory maps.
type fileStore struct {
	dir string
}

func newFileStore(t *testing.T) *fileStore {
	return &fileStore{dir: t.TempDir()}
}

func (fs *fileStore) path(id string) string {
	return filepath.Join(fs.dir, id+".snap")
}

func (fs *fileStore) Save(_ context.Context, id string, data []byte) error {
	return os.WriteFile(fs.path(id), data, 0o600)
}

func (fs *fileStore) Load(_ context.Context, id string) ([]byte, error) {
	data, err := os.ReadFile(fs.path(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func (fs *fileStore) Delete(_ context.Context, id string) error {
	err := os.Remove(fs.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func TestDiffStoreRoundTrip(t *testing.T) {
	store := newFileStore(t)
	ctx := context.Background()
	payload := []byte("snapshot-data-here")

	if err := store.Save(ctx, "sess-1", payload); err != nil {
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

func TestDiffStoreDeleteRemovesData(t *testing.T) {
	store := newFileStore(t)
	ctx := context.Background()

	if err := store.Save(ctx, "sess-2", []byte("data")); err != nil {
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

func TestDiffStoreLoadMissingReturnsNil(t *testing.T) {
	store := newFileStore(t)

	got, err := store.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if got != nil {
		t.Errorf("Load missing = %q, want nil", got)
	}
}

func TestDiffStoreDeleteMissingIsNoOp(t *testing.T) {
	store := newFileStore(t)

	if err := store.Delete(context.Background(), "nonexistent"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
}

func TestDiffStoreSaveOverwrites(t *testing.T) {
	store := newFileStore(t)
	ctx := context.Background()

	if err := store.Save(ctx, "sess-3", []byte("first")); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := store.Save(ctx, "sess-3", []byte("second")); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	got, err := store.Load(ctx, "sess-3")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got, []byte("second")) {
		t.Errorf("Load = %q, want %q", got, "second")
	}
}

// TestDiffStoreDisconnectPersists verifies the integration between
// a DiffStore and the session lifecycle: on transport close the
// differ snapshots are saved to the store and cleared from memory.
func TestDiffStoreDisconnectPersists(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newFileStore(t)
		mt := &mockTransport{
			events: []Event{{Type: "event", Action: "increment"}},
		}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.store = store

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// The transport delivered one event then EOF, triggering
		// onTransportClose. The store should now hold the snapshot.
		data, err := store.Load(context.Background(), sess.id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if data == nil {
			t.Fatal("expected snapshot data after disconnect, got nil")
		}

		// Clean up: cancel the context so the loop exits.
		sess.stop()
		synctest.Wait()
	})
}
