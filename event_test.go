package poly

import "testing"

func TestEventValue(t *testing.T) {
	ev := Event{Data: map[string]string{"value": "hello"}}
	if ev.Value() != "hello" {
		t.Errorf("Value() = %q, want %q", ev.Value(), "hello")
	}
}

func TestEventValueEmpty(t *testing.T) {
	ev := Event{}
	if ev.Value() != "" {
		t.Errorf("Value() = %q, want empty", ev.Value())
	}
}
