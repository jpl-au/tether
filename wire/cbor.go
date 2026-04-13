package wire

import (
	"github.com/fxamacker/cbor/v2"
)

// Compile-time check: CBOREncoder must satisfy Encoder.
var _ Encoder = CBOREncoder{}

// CBOREncoder encodes updates as CBOR (RFC 8949) using the same field
// names as the JSON encoder. The fxamacker/cbor library reads json
// struct tags when cbor tags are absent, so the [updateMessage] struct
// is shared between both encoders.
type CBOREncoder struct{}

// Encode serialises u as a CBOR map, omitting empty fields.
func (CBOREncoder) Encode(u Update) ([]byte, error) {
	msg := encodeMessage(u)
	return cbor.Marshal(msg)
}
