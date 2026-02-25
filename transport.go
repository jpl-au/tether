package poly

import (
	"errors"
	"time"
)

// ErrEventBufferFull is returned by a transport's PushEvent method
// when the internal event buffer is at capacity. PushEvent is
// non-blocking by design — if the session's event loop is not
// consuming events fast enough, the caller receives this error
// immediately rather than stalling the HTTP handler goroutine. The
// handler responds with HTTP 429 so the client can retry.
var ErrEventBufferFull = errors.New("event buffer full")

// Transport abstracts the persistent connection between server and
// client. The session event loop calls ReceiveEvent in a tight loop
// and calls Send after each state change. Implementations must be
// safe for concurrent use: Send may be called from any goroutine
// (via [Session.Update]), while ReceiveEvent is only called from the
// event loop goroutine.
//
// Send receives pre-encoded JSON bytes — the session handles all
// encoding so transports only deal with raw bytes.
//
// See the ws sub-package for WebSocket and the sse sub-package for
// Server-Sent Events.
type Transport interface {
	// Send writes pre-encoded JSON bytes to the client. The session
	// encodes updates before calling Send, so implementations only
	// need to frame and transmit the data.
	Send(data []byte) error

	// ReceiveEvent blocks until the next client event arrives. Returns
	// io.EOF when the connection is closed cleanly. Any other error is
	// treated as an unrecoverable connection failure and terminates the
	// session.
	ReceiveEvent() (Event, error)

	// Close terminates the connection. Must be safe to call from any
	// goroutine and safe to call more than once.
	Close() error
}

// eventPusher is an optional interface for transports that receive
// client events through a separate channel rather than through the
// transport connection itself. The SSE transport implements this
// because EventSource is unidirectional (server→client only) — client
// events arrive as HTTP POSTs and are routed to PushEvent by the
// handler. WebSocket transports do not need this because events
// arrive on the socket.
type eventPusher interface {
	PushEvent(Event) error
}

// heartbeater is an optional interface for transports that need
// periodic keep-alive writes to prevent intermediate proxies from
// closing idle connections. The SSE transport implements this; the
// WebSocket transport does not need it because the WebSocket protocol
// has its own ping/pong frames.
type heartbeater interface {
	StartHeartbeat(interval time.Duration)
}
