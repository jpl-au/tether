package poly

import (
	"encoding/json"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
)

func TestEncodeUpdateWithPatches(t *testing.T) {
	update := update{
		Patches: []jit.Patch{
			{Key: "count", HTML: []byte(`<span data-poly-key="count">42</span>`)},
			{Key: "name", HTML: []byte(`<span data-poly-key="name">Alice</span>`)},
		},
	}

	msg := encodeUpdate(update)

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

	// Verify it serialises to valid JSON
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

func TestEncodeUpdateWithMorphs(t *testing.T) {
	html := []byte(`<div data-poly-root><span>hello</span></div>`)
	update := update{
		Morphs: []morph{{Key: "", HTML: html}},
	}

	msg := encodeUpdate(update)

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

func TestEncodeUpdateWithURL(t *testing.T) {
	update := update{
		URL:     "/profile",
		Replace: false,
	}

	msg := encodeUpdate(update)

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

func TestEncodeUpdateWithURLReplace(t *testing.T) {
	update := update{
		URL:     "/current",
		Replace: true,
	}

	msg := encodeUpdate(update)

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

func TestEncodeUpdateWithSignals(t *testing.T) {
	update := update{
		Signals: map[string]any{
			"count":  42,
			"status": "online",
		},
	}

	msg := encodeUpdate(update)

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

func TestEncodeUpdateOmitsEmptySignals(t *testing.T) {
	update := update{
		Toast: "hello",
	}

	msg := encodeUpdate(update)

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

func TestEventUnmarshal(t *testing.T) {
	raw := `{"type":"click","action":"increment","data":{"value":"42"}}`

	var ev Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if ev.Type != "click" {
		t.Errorf("type should be %q, got %q", "click", ev.Type)
	}
	if ev.Action != "increment" {
		t.Errorf("action should be %q, got %q", "increment", ev.Action)
	}
	if ev.Data["value"] != "42" {
		t.Errorf("data.value should be %q, got %q", "42", ev.Data["value"])
	}
}

func TestEventUnmarshalNoData(t *testing.T) {
	raw := `{"type":"click","action":"toggle"}`

	var ev Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if ev.Action != "toggle" {
		t.Errorf("action should be %q, got %q", "toggle", ev.Action)
	}
	if ev.Data != nil {
		t.Error("data should be nil when omitted")
	}
}
