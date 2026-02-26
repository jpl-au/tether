package poly

import (
	"bytes"
	"encoding/json"

	jit "github.com/jpl-au/fluent-jit"
)

// update is the server-side representation of changes to send to the
// client. A single update can carry any combination of content patches
// (targeted key replacements), structural morphs (DOM mutations applied
// via idiomorph), URL changes (pushState/replaceState), and title
// changes. Combining them in one message lets the client apply
// everything atomically in a single pass.
type update struct {
	Patches  []jit.Patch
	Morphs   []morph
	URL      string            // if non-empty, push/replace browser URL
	Replace  bool              // true for replaceState, false for pushState
	Title    string            // if non-empty, set document.title
	Flash    map[string]string // key: CSS selector, value: plain text to display
	Signals  map[string]any    // key: signal name, value: pushed to bound elements
	Announce string            // if non-empty, inject into an aria-live region
	Toast    string            // if non-empty, show a global notification
	EventID  string            // echoed from the triggering Event for correlation
}

// morph represents a structural change that cannot be expressed as a
// simple content patch. The HTML replaces the content of the element
// identified by Key (a Dynamic key attribute), or the entire root
// element when Key is empty. The client applies morphs via idiomorph,
// which preserves focus, scroll position, and form state.
type morph struct {
	Key  string
	HTML []byte
}

// updateMessage is the JSON envelope sent over the wire. It mirrors
// update but uses string HTML (not []byte) and omits empty fields
// to keep the payload small.
//
// Patches and morphs are sent together so the client can apply them
// in a single pass — content patches first (cheap innerHTML swaps),
// then structural morphs (idiomorph reconciliation).
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
	EventID  string            `json:"event_id,omitempty"`
}

// patchEntry is a single key+html pair within an updateMessage. The
// Key identifies a Dynamic-keyed element in the DOM; the HTML replaces
// its innerHTML.
type patchEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// morphEntry is a single key+html pair for a structural morph within
// an updateMessage. An empty Key targets the root element.
type morphEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// encodeUpdate converts an update (server-side, []byte HTML) into an
// updateMessage (wire format, string HTML).
func encodeUpdate(u update) updateMessage {
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
	msg.EventID = u.EventID

	return msg
}

// marshalUpdate encodes an update as JSON with HTML escaping disabled.
// This is the single encoding path — called from Session.send() so
// transports only deal with raw bytes.
//
// HTML escaping is intentionally off because patches carry pre-rendered
// HTML from the fluent template engine, which is responsible for
// escaping user-provided values. No secondary sanitisation is performed
// at the transport layer — if the template engine produces unsafe HTML,
// it will reach the client as-is.
func marshalUpdate(u update) ([]byte, error) {
	msg := encodeUpdate(u)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(msg); err != nil {
		return nil, err
	}

	// Encode appends a trailing newline; strip it for clean JSON.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
