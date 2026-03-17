// Package protocol selects the HTTP protocol the server uses.
// Pass one of the constants to [tether.LiveConfig].Protocol.
//
//	tether.Live(tether.LiveConfig[State]{
//	    Protocol: protocol.HTTP2,
//	    // ...
//	})
//
// The default is [Auto], which detects the protocol from each
// request. Most applications should leave this unset.
package protocol

// Protocol identifies the HTTP protocol version. The framework uses
// this to adapt transport behaviour and emit appropriate warnings
// when the configured protocol doesn't match the wire protocol.
type Protocol int

const (
	// Auto detects the protocol from each request via r.ProtoMajor.
	// If r.TLS is nil, assumes HTTP/1.1 (browsers require TLS for
	// HTTP/2). This is the default and correct for most deployments
	// where a reverse proxy handles protocol negotiation.
	Auto Protocol = iota + 1

	// HTTP1 indicates HTTP/1.1. Use this when running behind a
	// proxy that downgrades HTTP/2 to HTTP/1.1.
	HTTP1

	// HTTP2 indicates HTTP/2. Use this when serving HTTPS directly
	// or behind an HTTP/2-aware proxy.
	HTTP2

	// HTTP3 indicates HTTP/3 (QUIC). Reserved for future use — the
	// Go ecosystem does not yet have standard library support for
	// HTTP/3. When support lands, this constant enables
	// QUIC-specific features.
	HTTP3
)

// String returns a human-readable label for the protocol.
func (p Protocol) String() string {
	switch p {
	case Auto:
		return "auto"
	case HTTP1:
		return "HTTP/1.1"
	case HTTP2:
		return "HTTP/2"
	case HTTP3:
		return "HTTP/3"
	default:
		return "unknown"
	}
}
