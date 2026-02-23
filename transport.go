package poly

import jit "github.com/jpl-au/fluent-jit"

// Transport abstracts the connection between server and client.
// See the ws sub-package for the WebSocket implementation.
type Transport interface {
	// SendPatches pushes targeted patches to the client.
	// Each patch contains a key and the new HTML for that element.
	SendPatches(patches []jit.Patch) error

	// SendFull pushes a complete re-render to the client.
	// Used on structural changes and reconnection.
	SendFull(html []byte) error

	// ReceiveEvent blocks until an event arrives from the client.
	// Returns io.EOF when the connection is closed.
	ReceiveEvent() (Event, error)

	// Close terminates the connection.
	Close() error
}
