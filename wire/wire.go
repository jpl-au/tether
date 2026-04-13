package wire

// Format selects the encoding used for updates sent from server to
// client. Pass one of the constants to [tether.StatefulConfig].WireFormat.
type Format int

const (
	// JSON encodes updates as a single JSON object containing
	// patches, morphs, signals, and side effects. This is the
	// default format.
	JSON Format = iota

	// CBOR encodes updates as a binary CBOR map (RFC 8949) using
	// the same field names as the JSON encoder. CBOR produces
	// smaller payloads and faster encoding at the cost of
	// human-readability.
	CBOR
)

// String returns the lowercase name of the wire format.
func (f Format) String() string {
	switch f {
	case CBOR:
		return "cbor"
	default:
		return "json"
	}
}
