package tether

// HandleFunc processes a client event and returns the new state. Side
// effects (toast, navigate, announce, flash, title, URL) are expressed
// as imperative calls on the session — there is no wrapper type. The
// session buffers effects during Handle and flushes them atomically
// with the state diff, so the client receives everything in one frame.
//
// The session parameter is a [PreSession] so that the same handler
// can be used in live mode, stateless page mode, and tethertest without
// changing its signature. In live mode the underlying value is a
// [*Session] which provides additional methods (Update, Go, Context,
// Close) via type assertion when needed.
//
// Returning the original state unchanged is valid and will produce no
// diff (especially when an Equal function is configured).
type HandleFunc[S any] func(session PreSession, state S, event Event) S
