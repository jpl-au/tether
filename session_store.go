package tether

import (
	"context"
	"time"
)

// SessionStore persists the developer's application state S (plus
// session metadata) for crash recovery and node migration. When a
// reconnecting client reaches a server that has no in-memory session,
// the framework checks the SessionStore before rejecting the
// reconnect - allowing sessions to survive server restarts.
//
// This is an opt-in capability. By default (nil on [StatefulConfig]),
// sessions live entirely in memory and a server restart loses all
// state. Set StatefulConfig.SessionStore to enable persistence.
//
// This interface is distinct from [DiffStore], which persists opaque
// differ snapshots as a memory optimisation. The two stores handle
// different data with different lifecycles and can be configured
// independently - a developer may use one without the other, both,
// or neither.
//
// The data passed to Save is an opaque envelope produced by the
// framework, containing the serialised state S and session metadata.
// Implementations must not interpret or modify the bytes.
//
// The framework does not ship any SessionStore implementations.
// Developers provide their own, backed by whatever storage suits
// their deployment (Redis, SQLite, filesystem, etc.).
//
// Implementations must be safe for concurrent use.
type SessionStore interface {
	// Save persists session data with a time-to-live hint. The
	// framework passes an appropriate TTL for each save context:
	// the reconnect window on disconnect, or a recovery window on
	// graceful shutdown. Implementations may use TTL for automatic
	// expiry (e.g. Redis SETEX), store it for periodic cleanup, or
	// ignore it entirely - the framework calls Delete when it can.
	// TTL is a safety net for orphaned data, not the primary
	// cleanup mechanism.
	Save(ctx context.Context, id string, data []byte, ttl time.Duration) error

	// Load retrieves previously saved session data. Returns the
	// data and nil on success. If the session ID is not found,
	// returns (nil, nil) - a missing entry is not an error. The
	// framework treats a miss as "no session to restore" and gives
	// the client a fresh session.
	Load(ctx context.Context, id string) ([]byte, error)

	// Delete removes session data. Called after a successful
	// reconnect (session is back in memory) or when a session is
	// destroyed. If the session ID is not found, Delete should be
	// a no-op and return nil.
	Delete(ctx context.Context, id string) error
}
