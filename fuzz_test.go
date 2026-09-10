package tether

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/tether/wire"
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

// FuzzEventDecode feeds arbitrary bytes to the client event decoder
// used by the HTTP, WebSocket and stateless handlers, then drives
// every accessor on whatever decodes. The bytes come straight from
// the browser, so decoding and reading must never panic.
func FuzzEventDecode(f *testing.F) {
	f.Add([]byte(`{"type":"click","action":"card.reorder","data":{"id":"7","index":"2","ok":"true","ratio":"0.5","wait":"5s"},"event_id":"1"}`))
	f.Add([]byte(`{"type":"input","data":{"value":"x"},"hashes":{"a":"1","b":"2"}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`{"data":null,"hashes":null}`))
	f.Add([]byte(`{"data":{"n":"99999999999999999999","f":"NaN","d":"-1ns"}}`))
	f.Add([]byte(`{"type":123,"data":"not a map"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			return
		}
		_ = ev.Value()
		_ = ev.Key()
		_, _ = ev.Get("id")
		_, _ = ev.Int("index")
		_, _ = ev.Float64("ratio")
		_ = ev.Bool("ok")
		_ = ev.WithAction("other")
		var form struct {
			ID    string        `tether:"id"`
			Index int           `tether:"index"`
			Ratio float64       `tether:"ratio"`
			OK    bool          `tether:"ok"`
			Wait  time.Duration `tether:"wait"`
			N     uint8         `tether:"n"`
			Name  string
		}
		_ = ev.Bind(&form)
		// A decoded event must re-encode: the handlers echo fields back.
		if _, err := json.Marshal(ev); err != nil {
			t.Fatalf("re-marshal of decoded event failed: %v", err)
		}
	})
}

// FuzzAutoFragmentUpdate drives the stateless auto-fragments path
// with a client-controlled hash map. The client can send any keys and
// any hashes; the server must never panic, must always return the
// complete fresh map, and must only ever morph keys that exist in the
// render or fall back to a full root morph.
func FuzzAutoFragmentUpdate(f *testing.F) {
	f.Add("a", "", "b", "", "", false)
	f.Add("a", "stale", "b", "stale", "", false)
	f.Add("a", "x", "b", "y", "extra", false)
	f.Add("", "", "", "", "", true)
	f.Add("a", "x", "a", "y", "", false)
	f.Add("../a", "\x00", "b\"", "</template>", "<script>", false)

	f.Fuzz(func(t *testing.T, k1, h1, k2, h2, extra string, empty bool) {
		one := span.Text("one")
		one.Dynamic("a")
		two := span.Text("two")
		two.Dynamic("b")
		tree := div.New(one, two)
		html, exts := renderPage(tree)
		fresh := fragmentHashes(fragments(html, exts))

		ev := Event{EventID: "9"}
		if !empty {
			ev.Hashes = map[string]string{k1: h1, k2: h2}
			if extra != "" {
				ev.Hashes[extra] = h1
			}
			// Empty seeds stand in for "the client echoed the real
			// hash", so the fuzzer can reach the unchanged path.
			for k, h := range ev.Hashes {
				if h == "" {
					if real, ok := fresh[k]; ok {
						ev.Hashes[k] = real
					}
				}
			}
		}

		u := autoFragmentUpdate(html, exts, ev)

		if u.EventID != "9" {
			t.Fatalf("event id not echoed: %q", u.EventID)
		}
		if len(u.Hashes) != len(fresh) {
			t.Fatalf("returned %d hashes, want %d", len(u.Hashes), len(fresh))
		}
		for k, h := range fresh {
			if u.Hashes[k] != h {
				t.Fatalf("hash for %q = %q, want %q", k, u.Hashes[k], h)
			}
		}
		for _, m := range u.Morphs {
			if m.Key == "" {
				if len(u.Morphs) != 1 || !bytes.Equal(m.HTML, html) {
					t.Fatalf("root morph must be the whole page and stand alone, got %d morphs", len(u.Morphs))
				}
				continue
			}
			if _, ok := fresh[m.Key]; !ok {
				t.Fatalf("morph for unknown key %q", m.Key)
			}
			if ev.Hashes[m.Key] == fresh[m.Key] {
				t.Fatalf("morph sent for unchanged key %q", m.Key)
			}
		}
		if _, _, err := wire.HTMLBody(u); err != nil {
			t.Fatalf("HTMLBody: %v", err)
		}
	})
}

// FuzzHTMLBody covers the HTML wire encoder, which embeds a JSON
// effects island inside the response body. Every effect value is
// application data - a toast from user input, a hash key from a
// Dynamic key - so nothing in the island may break out of the
// template element, and the island must always parse back.
func FuzzHTMLBody(f *testing.F) {
	f.Add("<p>hi</p>", "Saved", "Title", "/next", "k", "v", "a", "h", "sig")
	f.Add("", "", "", "", "", "", "", "", "")
	f.Add("<div>", "</template>", "</script>", "javascript:alert(1)", "</template>", "<", "\"", "\\", " ")
	f.Add("x", "\x00", "\xff", "", "", "", "", "", "")

	f.Fuzz(func(t *testing.T, html, toast, title, url, fk, fv, hk, hv, sig string) {
		u := wire.Update{
			Morphs:   []wire.Morph{{Key: "root", HTML: []byte(html)}},
			Toast:    toast,
			Title:    title,
			URL:      url,
			Announce: sig,
			Signals:  map[string]any{"s": sig},
		}
		if fk != "" {
			u.Flash = map[string]string{fk: fv}
		}
		if hk != "" {
			u.Hashes = map[string]string{hk: hv}
		}

		body, keyed, err := wire.HTMLBody(u)
		if err != nil {
			// Invalid UTF-8 is coerced by encoding/json; nothing here
			// should fail to marshal.
			t.Fatalf("HTMLBody: %v", err)
		}
		if !keyed {
			t.Fatal("keyed morph reported as root")
		}
		if !bytes.HasPrefix(body, []byte(html)) {
			t.Fatal("morph HTML not first in body")
		}

		open := []byte(`<template data-tether-effects>`)
		rest := body[len(html):]
		if len(rest) == 0 {
			return
		}
		if !bytes.HasPrefix(rest, open) || !bytes.HasSuffix(rest, []byte(`</template>`)) {
			t.Fatalf("malformed island: %q", rest)
		}
		island := rest[len(open) : len(rest)-len(`</template>`)]
		if bytes.ContainsAny(island, "<>") {
			t.Fatalf("island contains raw angle bracket: %q", island)
		}
		var got struct {
			Toast    string            `json:"toast"`
			Title    string            `json:"title"`
			URL      string            `json:"url"`
			Announce string            `json:"announce"`
			Flash    map[string]string `json:"flash"`
			Hashes   map[string]string `json:"hashes"`
		}
		if err := json.Unmarshal(island, &got); err != nil {
			t.Fatalf("island does not parse: %v\n%q", err, island)
		}
		// Valid UTF-8 must survive untouched; invalid bytes are
		// replaced by encoding/json, so only compare clean input.
		if utf8.ValidString(toast) && got.Toast != toast {
			t.Fatalf("toast round-trip: %q != %q", got.Toast, toast)
		}
		if utf8.ValidString(title) && got.Title != title {
			t.Fatalf("title round-trip: %q != %q", got.Title, title)
		}
		if fk != "" && utf8.ValidString(fk) && utf8.ValidString(fv) && got.Flash[fk] != fv {
			t.Fatalf("flash round-trip: %q != %q", got.Flash[fk], fv)
		}
		if hk != "" && utf8.ValidString(hk) && utf8.ValidString(hv) && got.Hashes[hk] != hv {
			t.Fatalf("hashes round-trip: %q != %q", got.Hashes[hk], hv)
		}
	})
}
