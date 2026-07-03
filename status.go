package tether

import "slices"

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
	// holds only its ID and metadata - S and the differ have been
	// released. Commands and effects are discarded with a
	// [CommandDiscarded] diagnostic. A reconnecting client thaws
	// the session by loading state from the store and starting a
	// new loop.
	Frozen

	// Destroyed means the session is permanently gone. The context
	// is cancelled, the loop has exited, and all resources have
	// been released.
	Destroyed
)

// validTransitions defines the allowed state machine edges. Any
// transition not listed here is a bug and will panic.
var validTransitions = map[Status][]Status{
	Pending: {Active},
	Active:  {Frozen, Destroyed},
	Frozen:  {Active, Destroyed},
}

// transition atomically moves the session to a new status. Panics
// if the transition is not valid according to the state machine.
// This makes invalid transitions impossible - bugs surface
// immediately during development rather than causing silent state
// corruption. A compare-and-swap loop (rather than load-then-store)
// keeps the check and the write atomic when two goroutines race,
// e.g. the loop freezing while Shutdown destroys.
func (s *StatefulSession[S]) transition(to Status) {
	for {
		from := Status(s.status.Load())
		if !slices.Contains(validTransitions[from], to) {
			panic("tether: invalid status transition " + from.String() + " -> " + to.String())
		}
		if s.status.CompareAndSwap(int32(from), int32(to)) {
			return
		}
	}
}

// String returns a human-readable name for the status.
func (s Status) String() string {
	switch s {
	case Pending:
		return "pending"
	case Active:
		return "active"
	case Frozen:
		return "frozen"
	case Destroyed:
		return "destroyed"
	default:
		return "unknown"
	}
}
