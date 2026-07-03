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

	// HTML sends updates as plain HTML: the morph fragments are the
	// response body and side effects ride in a small JSON island
	// appended to it. Responses are curl-inspectable and carry no
	// envelope overhead. Only supported by stateless handlers, whose
	// one-request-one-response shape fits an HTML body - stateful
	// transports multiplex many update kinds over one connection and
	// need the structured formats.
	HTML
)

// String returns the lowercase name of the wire format.
func (f Format) String() string {
	switch f {
	case CBOR:
		return "cbor"
	case HTML:
		return "html"
	default:
		return "json"
	}
}
