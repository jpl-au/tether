package tether

import "fmt"

// DiagnosticKind identifies the category of a framework diagnostic event.
type DiagnosticKind string

const (
	// TransportError signals a failure reading from or writing to the
	// session's transport (WebSocket or SSE). Normal disconnects
	// (io.EOF) are not emitted — only genuine failures.
	TransportError DiagnosticKind = "transport_error"

	// EncodeError signals a failure encoding a wire update (JSON
	// serialisation). This usually indicates a bug in the state or
	// render output (e.g. an unencodable type).
	EncodeError DiagnosticKind = "encode_error"

	// BufferOverflow signals that a session's command channel was full
	// and a goroutine was spawned to deliver the command. Sustained
	// overflow indicates a slow handler or broadcast storm.
	BufferOverflow DiagnosticKind = "buffer_overflow"

	// HandlerPanic signals a recovered panic inside Handle, Update,
	// or a command callback. The Err field contains the panic value.
	// This is also logged via slog as a critical safety net.
	HandlerPanic DiagnosticKind = "handler_panic"

	// UploadError signals a failure in an upload handler callback.
	UploadError DiagnosticKind = "upload_error"
)

// Diagnostic carries a framework-level event from the session lifecycle,
// transport layer, or command loop. Subscribe to [Handler.Diagnostics]
// to observe these events for metrics, alerting, or custom logging.
//
// Each diagnostic has a [DiagnosticKind] that identifies the category,
// an optional [Diagnostic.Err] with the underlying failure, and a
// [Diagnostic.SessionID] linking it to the affected session. Subscribers
// run synchronously by default — use [Bus.SubscribeAsync] for callbacks
// that perform I/O.
//
//	h.Diagnostics.Subscribe(ctx, func(d tether.Diagnostic) {
//	    switch d.Kind {
//	    case tether.HandlerPanic:
//	        alerting.Critical(d.SessionID, d.Err)
//	    case tether.TransportError:
//	        log.Warn("transport", "session", d.SessionID, "err", d.Err)
//	    case tether.BufferOverflow:
//	        metrics.Inc("tether.overflow")
//	    }
//	})
type Diagnostic struct {
	// Kind identifies the category of this diagnostic event.
	Kind DiagnosticKind

	// SessionID is the session that produced this event. Empty for
	// events that occur outside a session context (e.g. asset errors).
	SessionID string

	// Err is the underlying error, if any. Nil for informational
	// events like BufferOverflow where the count matters more than
	// the error.
	Err error

	// Detail provides human-readable context when the Kind and Err
	// alone are insufficient. For example, the event action that
	// triggered a panic, or the endpoint path.
	Detail string
}

// panicErr wraps a recovered panic value as an error.
func panicErr(v any) error {
	if err, ok := v.(error); ok {
		return err
	}
	return fmt.Errorf("%v", v)
}
