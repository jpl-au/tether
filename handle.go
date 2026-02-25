package poly

// HandleFunc processes a client event and returns the new state. Side
// effects (toast, navigate, announce, flash, title, URL) are expressed
// as imperative calls on the session — there is no wrapper type. The
// session buffers effects during Handle and flushes them atomically
// with the state diff, so the client receives everything in one frame.
//
// Call any Session method from within Handle — Update, Toast, Navigate,
// SetTitle, Announce, Flash, Close — without risk of deadlock. The
// command-loop architecture serialises all access.
//
// Returning the original state unchanged is valid and will produce no
// diff (especially when an Equal function is configured).
type HandleFunc[S any] func(session *Session[S], state S, event Event) S
