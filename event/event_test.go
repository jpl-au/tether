package event_test

import (
	"testing"

	"github.com/jpl-au/tether/event"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name string
		got  event.Type
		want string
	}{
		{"Click", event.Click, "click"},
		{"Input", event.Input, "input"},
		{"Submit", event.Submit, "submit"},
		{"Change", event.Change, "change"},
		{"KeyDown", event.KeyDown, "keydown"},
		{"Focus", event.Focus, "focus"},
		{"Blur", event.Blur, "blur"},
		{"Navigate", event.Navigate, "navigate"},
		{"Viewport", event.Viewport, "viewport"},
		{"Online", event.Online, "online"},
		{"Offline", event.Offline, "offline"},
		{"AppInstalled", event.AppInstalled, "appinstalled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.got) != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestTypeComparesToAnyEventName pins that Type is a plain defined
// string type, so an event bound with bind.On but with no constant here
// still compares directly in Handle. This is why no Custom constructor
// is needed.
func TestTypeComparesToAnyEventName(t *testing.T) {
	var got event.Type = "wheel"
	if got != "wheel" {
		t.Errorf("event.Type should compare to a bare event name, got %q", got)
	}
}
