package tether

import xport "github.com/jpl-au/tether/internal/transport"

// Transport abstracts the persistent connection between server and
// client. The session event loop calls ReceiveEvent in a tight loop
// and calls Send after each state change. Implementations must be
// safe for concurrent use: Send may be called from any goroutine
// (via [Session.Update]), while ReceiveEvent is only called from the
// event loop goroutine.
//
// Send receives pre-encoded bytes - the session handles all encoding
// (via [wire.Encoder]) so transports only deal with raw bytes.
//
// See the ws sub-package for WebSocket and the sse sub-package for
// Server-Sent Events.
type Transport = xport.Transport

// Heartbeater is an optional interface for transports that need
// periodic keep-alive activity. Both built-in transports implement
// it: SSE sends comment lines to prevent proxy timeouts; WebSocket
// sends ping frames and sets read deadlines to detect silently
// dropped connections.
//
// When the handler detects that a transport implements Heartbeater,
// it calls StartHeartbeat with the configured interval after the
// session is established.
type Heartbeater = xport.Heartbeater

// BinarySender is an optional interface for transports that can carry
// raw binary payloads (WebSocket binary frames). When a session's
// wire format is binary (CBOR) and its transport implements
// BinarySender, updates are sent as-is instead of base64-encoded.
type BinarySender = xport.BinarySender
