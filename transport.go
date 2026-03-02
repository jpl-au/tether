package tether

import "time"

// Transport abstracts the persistent connection between server and
// client. The session event loop calls ReceiveEvent in a tight loop
// and calls Send after each state change. Implementations must be
// safe for concurrent use: Send may be called from any goroutine
// (via [Session.Update]), while ReceiveEvent is only called from the
// event loop goroutine.
//
// Send receives pre-encoded bytes — the session handles all encoding
// (via [wire.Encoder]) so transports only deal with raw bytes.
//
// See the ws sub-package for WebSocket and the sse sub-package for
// Server-Sent Events.
type Transport interface {
	// Send writes pre-encoded bytes to the client. The session
	// encodes updates via [wire.Encoder] before calling Send, so
	// implementations only need to frame and transmit the data.
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

// heartbeater is an optional interface for transports that need
// periodic keep-alive writes to prevent intermediate proxies from
// closing idle connections. The SSE transport implements this; the
// WebSocket transport does not need it because the WebSocket protocol
// has its own ping/pong frames.
type heartbeater interface {
	StartHeartbeat(interval time.Duration)
}
