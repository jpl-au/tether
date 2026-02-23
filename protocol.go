package poly

import jit "github.com/jpl-au/fluent-jit"

// PatchMessage is the JSON envelope for targeted patches sent to the client.
// The server sends these when only specific keyed elements have changed.
type PatchMessage struct {
	Type    string       `json:"type"`
	Patches []PatchEntry `json:"patches"`
}

// PatchEntry is a single key+html pair within a patch message.
type PatchEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// FullMessage is the JSON envelope for a complete re-render sent to the client.
type FullMessage struct {
	Type string `json:"type"`
	HTML string `json:"html"`
}

// EncodePatch builds a PatchMessage from jit.Patch values.
// Transport implementations use this to create the wire format
// before marshalling to JSON.
func EncodePatch(patches []jit.Patch) PatchMessage {
	entries := make([]PatchEntry, len(patches))
	for i, p := range patches {
		entries[i] = PatchEntry{
			Key:  p.Key,
			HTML: string(p.HTML),
		}
	}
	return PatchMessage{
		Type:    "patch",
		Patches: entries,
	}
}

// EncodeFull builds a FullMessage from raw HTML bytes.
// Transport implementations use this to create the wire format
// before marshalling to JSON.
func EncodeFull(html []byte) FullMessage {
	return FullMessage{
		Type: "full",
		HTML: string(html),
	}
}
