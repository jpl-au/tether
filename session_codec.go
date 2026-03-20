package tether

import "github.com/fxamacker/cbor/v2"

// Compile-time check: cborCodec must satisfy SessionCodec.
var _ SessionCodec[struct{}] = cborCodec[struct{}]{}

// SessionCodec controls how session state S is serialised and
// deserialised for external storage. The framework uses this to
// convert S to bytes before wrapping it in a session envelope and
// passing it to [SessionStore].
//
// When nil on [StatefulConfig], the framework uses CBOR encoding (RFC 8949),
// which handles any struct with exported fields - no configuration,
// no struct tags, no boilerplate.
//
// Implement this when you need encryption, a company-standard wire
// format, or custom handling of complex types. The codec only handles
// S - it does not need to know about session IDs, URLs, or binding
// metadata (the framework wraps those separately in the envelope).
type SessionCodec[S any] interface {
	Marshal(state S) ([]byte, error)
	Unmarshal(data []byte) (S, error)
}

// cborCodec is the default SessionCodec. It uses CBOR (RFC 8949) via
// fxamacker/cbor/v2, which benchmarked best for encode speed and
// output size across gob, msgpack, CBOR, BSON, and binc.
//
// Constraints on S when using this default:
//   - Exported fields only (or fields with `cbor` struct tags)
//   - No channels, functions, or interface values
//   - No runtime handles (*sql.DB, *http.Client, etc.)
//
// State should be pure data. Runtime handles belong in lifecycle
// hooks (OnConnect, OnRestore), not in the state struct.
type cborCodec[S any] struct{}

func (cborCodec[S]) Marshal(state S) ([]byte, error) {
	return cbor.Marshal(state)
}

func (cborCodec[S]) Unmarshal(data []byte) (S, error) {
	var state S
	err := cbor.Unmarshal(data, &state)
	return state, err
}
