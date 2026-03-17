package tether

// Middleware wraps a [HandleFunc] to add cross-cutting behaviour.
// Each middleware receives the next handler in the chain and returns
// a new handler that may inspect or modify the event, state, or
// session before and after calling next:
//
//	func withLogging[S any](next tether.HandleFunc[S]) tether.HandleFunc[S] {
//	    return func(sess tether.Session, s S, ev tether.Event) S {
//	        slog.Info("event", "action", ev.Action)
//	        return next(sess, s, ev)
//	    }
//	}
//
// Middleware is applied outermost-first: the first middleware in the
// slice wraps the outermost layer of the chain. Use the Middleware
// field on [LiveConfig] to register middleware for all events.
type Middleware[S any] func(HandleFunc[S]) HandleFunc[S]

// Chain applies a slice of middleware to a handler in outermost-first
// order. Given [A, B, C] and handler H, the resulting call order is:
// A -> B -> C -> H.
func Chain[S any](h HandleFunc[S], mw []Middleware[S]) HandleFunc[S] {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
