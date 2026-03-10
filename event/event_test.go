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

func TestCustom(t *testing.T) {
	got := event.Custom("mouseover")
	if string(got) != "mouseover" {
		t.Errorf("Custom(%q) = %q, want %q", "mouseover", got, "mouseover")
	}
}
