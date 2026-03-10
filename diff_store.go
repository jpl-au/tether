package tether

import "context"

// DiffStore persists differ snapshots for disconnected sessions,
// allowing snapshot data to live outside process memory during the
// reconnect window. This is a memory optimisation — not a recovery
// mechanism. The framework calls Save when a session disconnects and
// Delete when it reconnects or is destroyed.
//
// This interface is distinct from [SessionStore], which persists the
// developer's application state S for crash recovery and node
// migration. The two stores handle different data with different
// lifecycles and can be configured independently.
//
// The data passed to Save is an opaque blob produced by the differ's
// Export method. Implementations must not interpret or modify the
// bytes — the encoding is an internal detail that may change between
// framework versions.
//
// Load is not called by the framework today — reconnecting sessions
// re-render from state, which re-seeds the differ. Load is included
// for tooling, debugging, and future optimisations.
//
// The framework does not ship any DiffStore implementations.
// Developers provide their own, backed by whatever storage suits
// their deployment (SQLite, Redis, filesystem, etc.). The default
// (nil on [Config]) keeps everything in memory.
//
// Implementations must be safe for concurrent use.
type DiffStore interface {
	// Save persists snapshot data for a disconnected session. The id
	// is the session ID. The data is an opaque blob from the differ.
	Save(ctx context.Context, id string, data []byte) error

	// Load retrieves previously saved snapshot data. Returns the data
	// and nil on success. If the session ID is not found, returns
	// (nil, nil) — a missing entry is not an error. The framework
	// treats a missing entry the same as a Load failure: the client
	// gets a fresh session.
	Load(ctx context.Context, id string) ([]byte, error)

	// Delete removes snapshot data for a session. Called after a
	// successful reconnect (data is back in memory) or when a
	// session expires. If the session ID is not found, Delete
	// should be a no-op and return nil.
	Delete(ctx context.Context, id string) error
}
