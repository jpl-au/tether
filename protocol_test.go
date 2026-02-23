package poly

import (
	"encoding/json"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
)

func TestEncodePatch(t *testing.T) {
	patches := []jit.Patch{
		{Key: "count", HTML: []byte(`<span data-poly-key="count">42</span>`)},
		{Key: "name", HTML: []byte(`<span data-poly-key="name">Alice</span>`)},
	}

	msg := EncodePatch(patches)

	if msg.Type != "patch" {
		t.Errorf("type should be %q, got %q", "patch", msg.Type)
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

	// Verify it serialises to valid JSON
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["type"] != "patch" {
		t.Errorf("decoded type should be %q, got %v", "patch", decoded["type"])
	}
}

func TestEncodeFull(t *testing.T) {
	html := []byte(`<div data-poly-root><span>hello</span></div>`)
	msg := EncodeFull(html)

	if msg.Type != "full" {
		t.Errorf("type should be %q, got %q", "full", msg.Type)
	}
	if msg.HTML != string(html) {
		t.Errorf("HTML mismatch: got %q", msg.HTML)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["type"] != "full" {
		t.Errorf("decoded type should be %q, got %v", "full", decoded["type"])
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
