package poly

// Transport abstracts the connection between server and client.
// See the ws sub-package for the WebSocket implementation.
type Transport interface {
	// SendUpdate pushes an update to the client. The update may contain
	// content patches, structural morphs, or both.
	SendUpdate(update Update) error

	// ReceiveEvent blocks until an event arrives from the client.
	// Returns io.EOF when the connection is closed.
	ReceiveEvent() (Event, error)

	// Close terminates the connection.
	Close() error
}

// EventPusher is implemented by transports that receive client events
// through an external channel (e.g. HTTP POST) rather than through
// the transport connection itself (e.g. WebSocket frames). The handler
// uses this to route incoming POST requests to the correct session.
type EventPusher interface {
	PushEvent(Event) error
}
