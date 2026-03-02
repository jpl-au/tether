// Package wire defines the encoding abstraction for server-to-client
// updates. The [Format] type selects which [Encoder] implementation
// serialises updates into bytes for the transport layer.
//
// Currently the only supported format is [JSON]. Additional formats
// (e.g. HTML fragments) will be added in future.
package wire

// Format selects the encoding used for updates sent from server to
// client. Pass one of the constants to [poly.Config].WireFormat.
type Format int

const (
	// JSON encodes updates as a single JSON object containing
	// patches, morphs, signals, and side effects. This is the
	// default format.
	JSON Format = iota
)
