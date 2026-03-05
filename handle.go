package tether

// HandleFunc processes a client event and returns the new state. Side
// effects (toast, navigate, announce, flash, title, URL) are expressed
// as imperative calls on the session — there is no wrapper type. The
// session buffers effects during Handle and flushes them atomically
// with the state diff, so the client receives everything in one frame.
//
// Handle runs inside the session's command loop. While it is executing,
// no other commands, events, or effects are processed for this session.
// Keep Handle fast — do not perform blocking I/O, sleep, or wait on
// channels. For slow operations, use [Session.Go] to run them in a
// background goroutine and feed results back via [Session.Update].
//
// The session parameter is a [Session] so that the same handler
// can be used in live mode, stateless page mode, and tethertest without
// changing its signature. In live mode the underlying value is a
// [*LiveSession] which provides additional methods (Update, Go, Context,
// Close) via type assertion when needed.
//
// Returning the original state unchanged is valid and will produce no
// diff (especially when an Equal function is configured).
type HandleFunc[S any] func(session Session, state S, event Event) S
