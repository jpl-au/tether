package wire

import (
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func TestCBOREncoderRoundTrip(t *testing.T) {
	u := Update{
		Patches: []Patch{
			{Key: "count", HTML: []byte(`<span>42</span>`)},
		},
		Signals: map[string]any{"count": 42},
		Toast:   "saved",
		EventID: "ev-1",
	}

	enc := CBOREncoder{}
	data, err := enc.Encode(u)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	var decoded map[string]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CBOR: %v", err)
	}

	if decoded["type"] != "update" {
		t.Errorf("type = %v, want update", decoded["type"])
	}
	if decoded["toast"] != "saved" {
		t.Errorf("toast = %v, want saved", decoded["toast"])
	}
	if decoded["event_id"] != "ev-1" {
		t.Errorf("event_id = %v, want ev-1", decoded["event_id"])
	}

	patches, ok := decoded["patches"].([]any)
	if !ok || len(patches) != 1 {
		t.Fatalf("expected 1 patch, got %v", decoded["patches"])
	}
}

// TestCBOREncoderOmitsEmpty verifies that the CBOR encoder respects
// omitempty from the json struct tags, keeping payloads compact.
func TestCBOREncoderOmitsEmpty(t *testing.T) {
	u := Update{Toast: "hello"}

	enc := CBOREncoder{}
	data, err := enc.Encode(u)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	var decoded map[string]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CBOR: %v", err)
	}

	for _, key := range []string{"patches", "morphs", "signals", "scroll_to", "download"} {
		if _, ok := decoded[key]; ok {
			t.Errorf("%s should be omitted when empty", key)
		}
	}
}

// TestCBORFieldNames verifies that the CBOR encoder uses the same
// field names as the JSON encoder. The client decodes both formats
// with the same key lookup, so the names must match exactly.
func TestCBORFieldNames(t *testing.T) {
	u := Update{
		Patches:  []Patch{{Key: "k", HTML: []byte("<b>v</b>")}},
		Morphs:   []Morph{{Key: "", HTML: []byte("<div>root</div>")}},
		Session:  "sess-1",
		URL:      "/page",
		Replace:  true,
		Title:    "Title",
		Flash:    map[string]string{"info": "ok"},
		Signals:  map[string]any{"x": 1},
		Announce: "announced",
		Toast:    "toasted",
		ScrollTo: "#top",
		Download: "/file.csv",
		EventID:  "ev-42",
	}

	enc := CBOREncoder{}
	data, err := enc.Encode(u)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	var decoded map[string]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CBOR: %v", err)
	}

	want := []string{
		"type", "patches", "morphs", "session", "url", "replace",
		"title", "flash", "signals", "announce", "toast",
		"scroll_to", "download", "event_id",
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing expected key %q in CBOR output", key)
		}
	}
}
