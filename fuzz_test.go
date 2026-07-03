package tether

import (
	"strings"
	"testing"
)

// FuzzValidSessionID checks the property the validator exists for: no
// accepted ID can carry path or encoding metacharacters that would be
// dangerous as a filename or store key, and generated IDs always pass.
func FuzzValidSessionID(f *testing.F) {
	f.Add(newID())
	f.Add("")
	f.Add("../../etc/passwd")
	f.Add("..%2f..%2fsecret")
	f.Add("a b")
	f.Add("with.dot")
	f.Add(strings.Repeat("A", 129))
	f.Add("ok-with-hyphen_and_underscore")

	f.Fuzz(func(t *testing.T, id string) {
		if !validSessionID(id) {
			return
		}
		if id == "" || len(id) > 128 {
			t.Fatalf("accepted ID with invalid length %d", len(id))
		}
		if strings.ContainsAny(id, "/\\.%\x00 \t\r\n?&=#") {
			t.Fatalf("accepted ID with unsafe character: %q", id)
		}
	})
}

// FuzzUnmarshalEnvelope feeds arbitrary bytes to the session-store
// envelope decoder. Store contents are attacker-adjacent (a shared
// store, a tampered file), so decoding must fail cleanly - never
// panic - on garbage.
func FuzzUnmarshalEnvelope(f *testing.F) {
	valid, err := marshalEnvelope(sessionEnvelope{
		State:     []byte("state"),
		Endpoint:  "/app",
		URL:       "/app?p=1",
		Title:     "Title",
		UserAgent: "UA/1.0",
	})
	if err != nil {
		f.Fatalf("marshalEnvelope: %v", err)
	}
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff})
	f.Add([]byte("not cbor at all"))

	f.Fuzz(func(t *testing.T, data []byte) {
		env, err := unmarshalEnvelope(data)
		if err != nil {
			return
		}
		// A decoded envelope must round-trip without panicking.
		if _, err := marshalEnvelope(env); err != nil {
			t.Fatalf("re-marshal of decoded envelope failed: %v", err)
		}
	})
}

// FuzzUnmarshalValueEnvelope covers the cluster Value envelope - the
// bytes arrive from the cluster broker, which other nodes (and anyone
// who can publish to it) write to.
func FuzzUnmarshalValueEnvelope(f *testing.F) {
	valid, err := marshalEnvelopeCBOR(valueEnvelope{NodeID: "node-1", Data: []byte{0x01}})
	if err != nil {
		f.Fatalf("marshalEnvelopeCBOR: %v", err)
	}
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{0xa1, 0x61, 0x6e})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic; errors are the expected failure mode.
		_, _ = unmarshalValueEnvelope(data)
	})
}
