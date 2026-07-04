package wire

import (
	"bytes"
	"encoding/json"
)

// HTMLBody renders u as an HTML response body for the [HTML] wire
// format: the morph fragments concatenated in order, followed by a
// JSON effects island when the update carries side effects:
//
//	<div data-fluent-key="list">...</div>
//	<template data-tether-effects>{"toast":"Saved"}</template>
//
// The island uses the same field names as the JSON wire format, and
// standard JSON escaping keeps "</template>" from appearing inside
// it. keyed reports whether the morphs are targeted keyed fragments
// rather than a single root morph - the caller signals this to the
// client via the Tether-Morph response header, because a root render
// whose top-level elements all happen to carry keys would otherwise
// be indistinguishable from a fragment response.
func HTMLBody(u Update) (body []byte, keyed bool, err error) {
	var buf bytes.Buffer
	for _, m := range u.Morphs {
		buf.Write(m.HTML)
	}
	keyed = len(u.Morphs) > 0 && u.Morphs[0].Key != ""

	fx, ok, err := encodeEffects(u)
	if err != nil {
		return nil, false, err
	}
	if ok {
		buf.WriteString(`<template data-tether-effects>`)
		buf.Write(fx)
		buf.WriteString(`</template>`)
	}
	return buf.Bytes(), keyed, nil
}

// effectsMessage carries only the side-effect fields of an update -
// no patches, morphs, session, or event ID - using the same JSON
// names as [updateMessage] so the client merges it straight into the
// message it builds from the HTML body.
type effectsMessage struct {
	Hashes   map[string]string `json:"hashes,omitempty"`
	URL      string            `json:"url,omitempty"`
	Replace  bool              `json:"replace,omitempty"`
	Title    string            `json:"title,omitempty"`
	Flash    map[string]string `json:"flash,omitempty"`
	Signals  map[string]any    `json:"signals,omitempty"`
	Announce string            `json:"announce,omitempty"`
	Toast    string            `json:"toast,omitempty"`
	ScrollTo string            `json:"scroll_to,omitempty"`
	Download string            `json:"download,omitempty"`
}

// encodeEffects serialises the side-effect fields of u. ok is false
// when the update carries none, so callers can omit the island
// entirely. Unlike the transport encoders, HTML escaping stays ON -
// the JSON is embedded inside an HTML template element.
func encodeEffects(u Update) (data []byte, ok bool, err error) {
	if u.URL == "" && u.Title == "" && len(u.Flash) == 0 && len(u.Signals) == 0 &&
		u.Announce == "" && u.Toast == "" && u.ScrollTo == "" && u.Download == "" &&
		u.Hashes == nil {
		return nil, false, nil
	}
	data, err = json.Marshal(effectsMessage{
		Hashes:   u.Hashes,
		URL:      u.URL,
		Replace:  u.Replace,
		Title:    u.Title,
		Flash:    u.Flash,
		Signals:  u.Signals,
		Announce: u.Announce,
		Toast:    u.Toast,
		ScrollTo: u.ScrollTo,
		Download: u.Download,
	})
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
