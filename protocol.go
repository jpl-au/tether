package poly

import jit "github.com/jpl-au/fluent-jit"

// Update is the server-side representation of changes to send to the
// client. A single Update can carry any combination of content patches
// (targeted key replacements), structural morphs (DOM mutations applied
// via idiomorph), URL changes (pushState/replaceState), and title
// changes. Combining them in one message lets the client apply
// everything atomically in a single pass.
type Update struct {
	Patches []jit.Patch
	Morphs  []Morph
	URL     string            // if non-empty, push/replace browser URL
	Replace bool              // true for replaceState, false for pushState
	Title   string            // if non-empty, set document.title
	Flash   map[string]string // key: target element selector, value: HTML/text to display
	EventID string            // echoed from the triggering Event for correlation
}

// Morph represents a structural change that cannot be expressed as a
// simple content patch. The HTML replaces the content of the element
// identified by Key (a Dynamic key attribute), or the entire root
// element when Key is empty. The client applies morphs via idiomorph,
// which preserves focus, scroll position, and form state.
type Morph struct {
	Key  string
	HTML []byte
}

// UpdateMessage is the JSON envelope sent over the wire. It mirrors
// [Update] but uses string HTML (not []byte) and omits empty fields
// to keep the payload small. Transport implementations convert an
// Update to an UpdateMessage via [EncodeUpdate] before marshalling.
//
// Patches and morphs are sent together so the client can apply them
// in a single pass — content patches first (cheap innerHTML swaps),
// then structural morphs (idiomorph reconciliation).
type UpdateMessage struct {
	Type    string            `json:"type"`
	Patches []PatchEntry      `json:"patches,omitempty"`
	Morphs  []MorphEntry      `json:"morphs,omitempty"`
	URL     string            `json:"url,omitempty"`
	Replace bool              `json:"replace,omitempty"`
	Title   string            `json:"title,omitempty"`
	Flash   map[string]string `json:"flash,omitempty"`
	EventID string            `json:"event_id,omitempty"`
}

// PatchEntry is a single key+html pair within an [UpdateMessage]. The
// Key identifies a Dynamic-keyed element in the DOM; the HTML replaces
// its innerHTML.
type PatchEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// MorphEntry is a single key+html pair for a structural morph within
// an [UpdateMessage]. An empty Key targets the root element.
type MorphEntry struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// EncodeUpdate converts an [Update] (server-side, []byte HTML) into an
// [UpdateMessage] (wire format, string HTML). Transport implementations
// call this before marshalling to JSON.
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
	msg.Title = update.Title
	msg.Flash = update.Flash
	msg.EventID = update.EventID

	return msg
}
