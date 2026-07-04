package wire

import (
	"encoding/json"
	"testing"
)

func TestEncodeWithPatches(t *testing.T) {
	u := Update{
		Patches: []Patch{
			{Key: "count", HTML: []byte(`<span data-fluent-key="count">42</span>`)},
			{Key: "name", HTML: []byte(`<span data-fluent-key="name">Alice</span>`)},
		},
	}

	msg := encodeMessage(u)

	if msg.Type != "update" {
		t.Errorf("type should be %q, got %q", "update", msg.Type)
	}
	if len(msg.Patches) != 2 {
		t.Fatalf("expected 2 patches, got %d", len(msg.Patches))
	}
	if msg.Patches[0].Key != "count" {
		t.Errorf("first patch key should be %q, got %q", "count", msg.Patches[0].Key)
	}
	if msg.Patches[1].Key != "name" {
		t.Errorf("second patch key should be %q, got %q", "name", msg.Patches[1].Key)
	}
	if msg.Morphs != nil {
		t.Error("patches-only update should have nil morphs")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["type"] != "update" {
		t.Errorf("decoded type should be %q, got %v", "update", decoded["type"])
	}
}

func TestEncodeWithMorphs(t *testing.T) {
	html := []byte(`<div data-tether-root><span>hello</span></div>`)
	u := Update{
		Morphs: []Morph{{Key: "", HTML: html}},
	}

	msg := encodeMessage(u)

	if msg.Type != "update" {
		t.Errorf("type should be %q, got %q", "update", msg.Type)
	}
	if len(msg.Morphs) != 1 {
		t.Fatalf("expected 1 morph, got %d", len(msg.Morphs))
	}
	if msg.Morphs[0].Key != "" {
		t.Errorf("root morph key should be empty, got %q", msg.Morphs[0].Key)
	}
	if msg.Morphs[0].HTML != string(html) {
		t.Errorf("morph HTML mismatch: got %q", msg.Morphs[0].HTML)
	}
	if msg.Patches != nil {
		t.Error("morphs-only update should have nil patches")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["type"] != "update" {
		t.Errorf("decoded type should be %q, got %v", "update", decoded["type"])
	}
}

func TestEncodeWithURL(t *testing.T) {
	u := Update{URL: "/profile"}

	msg := encodeMessage(u)

	if msg.URL != "/profile" {
		t.Errorf("URL should be %q, got %q", "/profile", msg.URL)
	}
	if msg.Replace {
		t.Error("Replace should be false")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["url"] != "/profile" {
		t.Errorf("decoded url should be %q, got %v", "/profile", decoded["url"])
	}
}

func TestEncodeWithURLReplace(t *testing.T) {
	u := Update{URL: "/current", Replace: true}

	msg := encodeMessage(u)

	if msg.URL != "/current" {
		t.Errorf("URL should be %q, got %q", "/current", msg.URL)
	}
	if !msg.Replace {
		t.Error("Replace should be true")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["replace"] != true {
		t.Errorf("decoded replace should be true, got %v", decoded["replace"])
	}
}

func TestEncodeWithSignals(t *testing.T) {
	u := Update{
		Signals: map[string]any{
			"count":  42,
			"status": "online",
		},
	}

	msg := encodeMessage(u)

	if msg.Type != "update" {
		t.Errorf("type should be %q, got %q", "update", msg.Type)
	}
	if msg.Signals == nil {
		t.Fatal("signals should not be nil")
	}
	if msg.Signals["count"] != 42 {
		t.Errorf("signals[count] should be 42, got %v", msg.Signals["count"])
	}
	if msg.Signals["status"] != "online" {
		t.Errorf("signals[status] should be %q, got %v", "online", msg.Signals["status"])
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	signals, ok := decoded["signals"].(map[string]any)
	if !ok {
		t.Fatal("decoded signals should be a map")
	}
	// JSON numbers decode as float64.
	if signals["count"] != float64(42) {
		t.Errorf("decoded signals[count] = %v, want 42", signals["count"])
	}
	if signals["status"] != "online" {
		t.Errorf("decoded signals[status] = %v, want online", signals["status"])
	}
}

func TestEncodeOmitsEmptySignals(t *testing.T) {
	u := Update{Toast: "hello"}

	msg := encodeMessage(u)

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, ok := decoded["signals"]; ok {
		t.Error("signals key should be omitted when empty")
	}
}

func TestEncodeScrollTo(t *testing.T) {
	u := Update{ScrollTo: "#card-5"}
	msg := encodeMessage(u)

	if msg.ScrollTo != "#card-5" {
		t.Errorf("ScrollTo = %q, want #card-5", msg.ScrollTo)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["scroll_to"] != "#card-5" {
		t.Errorf("decoded scroll_to = %v, want #card-5", decoded["scroll_to"])
	}
}

func TestEncodeOmitsEmptyScrollTo(t *testing.T) {
	u := Update{Toast: "hello"}
	data, err := json.Marshal(encodeMessage(u))
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, ok := decoded["scroll_to"]; ok {
		t.Error("scroll_to should be omitted when empty")
	}
}

func TestEncodeDownload(t *testing.T) {
	u := Update{Download: "/export/report.csv"}
	msg := encodeMessage(u)

	if msg.Download != "/export/report.csv" {
		t.Errorf("Download = %q, want /export/report.csv", msg.Download)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["download"] != "/export/report.csv" {
		t.Errorf("decoded download = %v, want /export/report.csv", decoded["download"])
	}
}

func TestEncodeOmitsEmptyDownload(t *testing.T) {
	u := Update{Toast: "hello"}
	data, err := json.Marshal(encodeMessage(u))
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, ok := decoded["download"]; ok {
		t.Error("download should be omitted when empty")
	}
}

func TestJSONEncoderRoundTrip(t *testing.T) {
	u := Update{
		Patches: []Patch{
			{Key: "count", HTML: []byte(`<span>42</span>`)},
		},
		Signals: map[string]any{"count": 42},
		Toast:   "saved",
		EventID: "ev-1",
	}

	enc := JSONEncoder{}
	data, err := enc.Encode(u)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
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
