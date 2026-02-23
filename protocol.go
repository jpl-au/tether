package poly

import jit "github.com/jpl-au/fluent-jit"

// Update is the server-side representation of changes to send to the client.
// It contains content patches (targeted key updates) and/or morphs
// (structural DOM changes applied via idiomorph). URL fields allow
// the server to push browser URL changes alongside DOM updates.
type Update struct {
	Patches []jit.Patch
	Morphs  []Morph
	URL     string // if non-empty, push/replace browser URL
	Replace bool   // true for replaceState, false for pushState
}

// Morph represents a structural change to a keyed container or the root.
// An empty Key targets the root element.
type Morph struct {
	Key  string
	HTML []byte
}

// UpdateMessage is the JSON envelope sent over the wire.
// Patches and morphs are sent together so the client can apply them
// in a single pass — content updates first, then structural changes.
// URL fields allow the server to push browser URL changes.
type UpdateMessage struct {
	Type    string       `json:"type"`
	Patches []PatchEntry `json:"patches,omitempty"`
	Morphs  []MorphEntry `json:"morphs,omitempty"`
	URL     string       `json:"url,omitempty"`
	Replace bool         `json:"replace,omitempty"`
}

// PatchEntry is a single key+html pair within an update message.
type PatchEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// MorphEntry is a single key+html pair for a structural morph.
// An empty key targets the root element.
type MorphEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// EncodeUpdate builds an UpdateMessage from an Update.
// Transport implementations use this to create the wire format
// before marshalling to JSON.
func EncodeUpdate(update Update) UpdateMessage {
	msg := UpdateMessage{Type: "update"}

	if len(update.Patches) > 0 {
		msg.Patches = make([]PatchEntry, len(update.Patches))
		for i, p := range update.Patches {
			msg.Patches[i] = PatchEntry{
				Key:  p.Key,
				HTML: string(p.HTML),
			}
		}
	}

	if len(update.Morphs) > 0 {
		msg.Morphs = make([]MorphEntry, len(update.Morphs))
		for i, m := range update.Morphs {
			msg.Morphs[i] = MorphEntry{
				Key:  m.Key,
				HTML: string(m.HTML),
			}
		}
	}

	msg.URL = update.URL
	msg.Replace = update.Replace

	return msg
}
