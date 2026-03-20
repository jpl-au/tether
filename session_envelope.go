package tether

import "github.com/fxamacker/cbor/v2"

// sessionEnvelope wraps the codec-encoded state bytes with session
// metadata that the framework needs to restore a session on crash
// recovery or node migration. The envelope is what gets passed to
// SessionStore - the store sees opaque bytes and never interprets
// the contents.
//
// The codec handles S (the developer's state). The envelope handles
// everything else: where the session was, what it was showing, and
// who owned it. This separation keeps the codec focused on a single
// concern.
type sessionEnvelope struct {
	// State holds the codec-encoded bytes of the developer's state S.
	State []byte `cbor:"state"`

	// Endpoint is the URL path the session was created on (from the
	// initial GET). Used to route the restored session to the correct
	// page handler.
	Endpoint string `cbor:"endpoint"`

	// URL is the last URL sent to the client. Replayed on restore so
	// the browser's address bar is correct.
	URL string `cbor:"url"`

	// Title is the last document title sent to the client. Replayed
	// on restore so the browser tab shows the correct title.
	Title string `cbor:"title"`

	// UserAgent is the User-Agent header captured when the session
	// was created. Verified on restore to detect stolen session IDs
	// - a reconnecting client must present the same User-Agent.
	UserAgent string `cbor:"user_agent"`
}

// marshalEnvelope serialises an envelope to bytes for storage.
func marshalEnvelope(env sessionEnvelope) ([]byte, error) {
	return cbor.Marshal(env)
}

// unmarshalEnvelope deserialises bytes from storage into an envelope.
func unmarshalEnvelope(data []byte) (sessionEnvelope, error) {
	var env sessionEnvelope
	err := cbor.Unmarshal(data, &env)
	return env, err
}
