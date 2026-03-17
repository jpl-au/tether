package tether

// Status represents the lifecycle state of a session. Each session
// transitions through these states exactly once in forward order,
// except Frozen which can return to Active on reconnect.
//
//	Pending → Active → Frozen → Active (thaw on reconnect)
//	                 → Destroyed (timeout, shutdown, or explicit close)
//	          Active → Destroyed (direct, no reconnect)
type Status int32

const (
	// Pending means the session has been created (pre-warmed on
	// initial GET) but the transport has not yet connected.
	Pending Status = iota + 1

	// Active means the session's command loop is running and a
	// transport may be attached. Commands, effects, and events
	// are all processed.
	Active

	// Frozen means the session's state has been persisted to the
	// SessionStore and the command loop has exited. The session
	// holds only its ID and metadata — S and the differ have been
	// released. Commands and effects are silently discarded.
	// A reconnecting client thaws the session by loading state
	// from the store and starting a new loop.
	Frozen

	// Destroyed means the session is permanently gone. The context
	// is cancelled, the loop has exited, and all resources have
	// been released.
	Destroyed
)
