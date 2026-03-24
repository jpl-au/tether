package wire

import (
	"bytes"
	"encoding/json"
)

// Compile-time check: JSONEncoder must satisfy Encoder.
var _ Encoder = JSONEncoder{}

// JSONEncoder encodes updates as JSON with HTML escaping disabled.
// HTML escaping is intentionally off because patches carry
// pre-rendered HTML from the fluent template engine, which is
// responsible for escaping user-provided values. No secondary
// sanitisation is performed at the transport layer.
type JSONEncoder struct{}

// Encode serialises u as a compact JSON object, omitting empty fields.
func (JSONEncoder) Encode(u Update) ([]byte, error) {
	msg := encodeMessage(u)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(msg); err != nil {
		return nil, err
	}

	// Encode appends a trailing newline; strip it for clean JSON.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// updateMessage is the JSON envelope sent over the wire. It mirrors
// [Update] but uses string HTML (not []byte) and omits empty fields
// to keep the payload small.
type updateMessage struct {
	Type     string            `json:"type"`
	Patches  []patchEntry      `json:"patches,omitempty"`
	Morphs   []morphEntry      `json:"morphs,omitempty"`
	URL      string            `json:"url,omitempty"`
	Replace  bool              `json:"replace,omitempty"`
	Title    string            `json:"title,omitempty"`
	Flash    map[string]string `json:"flash,omitempty"`
	Signals  map[string]any    `json:"signals,omitempty"`
	Announce string            `json:"announce,omitempty"`
	Toast    string            `json:"toast,omitempty"`
	ScrollTo string            `json:"scroll_to,omitempty"`
	Download string            `json:"download,omitempty"`
	EventID  string            `json:"event_id,omitempty"`
}

// patchEntry is a single key+html pair within an [updateMessage].
type patchEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// morphEntry is a single key+html pair for a structural morph within
// an [updateMessage]. An empty Key targets the root element.
type morphEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// encodeMessage converts a format-agnostic [Update] into the
// JSON-specific [updateMessage].
func encodeMessage(u Update) updateMessage {
	msg := updateMessage{Type: "update"}

	if len(u.Patches) > 0 {
		msg.Patches = make([]patchEntry, len(u.Patches))
		for i, p := range u.Patches {
			msg.Patches[i] = patchEntry{
				Key:  p.Key,
				HTML: string(p.HTML),
			}
		}
	}

	if len(u.Morphs) > 0 {
		msg.Morphs = make([]morphEntry, len(u.Morphs))
		for i, m := range u.Morphs {
			msg.Morphs[i] = morphEntry{
				Key:  m.Key,
				HTML: string(m.HTML),
			}
		}
	}

	msg.URL = u.URL
	msg.Replace = u.Replace
	msg.Title = u.Title
	msg.Flash = u.Flash
	msg.Signals = u.Signals
	msg.Announce = u.Announce
	msg.Toast = u.Toast
	msg.ScrollTo = u.ScrollTo
	msg.Download = u.Download
	msg.EventID = u.EventID

	return msg
}
