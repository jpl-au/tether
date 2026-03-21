package tethertest

import (
	tether "github.com/jpl-au/tether"
	"github.com/jpl-au/tether/event"
)

// ComponentHarness drives a [tether.Component] in isolation for testing.
// Events are dispatched directly to the component's Handle method  -
// no prefix stripping, no wrapper state, no transport plumbing.
//
//	h := tethertest.NewComponent(MyWidget{Count: 0})
//	h.Send("inc")
//	if h.Component().Count != 1 {
//	    t.Errorf("got %d, want 1", h.Component().Count)
//	}
type ComponentHarness[C tether.Component] struct {
	comp C
	last tether.Effects
}

// NewComponent creates a test harness for an isolated [tether.Component].
// The component receives events directly - no prefix, no parent state,
// no getter/setter boilerplate.
func NewComponent[C tether.Component](initial C) *ComponentHarness[C] {
	return &ComponentHarness[C]{comp: initial}
}

// Send fires a click event with the given action.
func (h *ComponentHarness[C]) Send(action string) {
	h.SendEvent(tether.Event{
		Type:   event.Click,
		Action: action,
		Data:   map[string]string{},
	})
}

// SendInput fires an input event with the given action and value.
func (h *ComponentHarness[C]) SendInput(action, value string) {
	h.SendEvent(tether.Event{
		Type:   event.Input,
		Action: action,
		Data:   map[string]string{"value": value},
	})
}

// SendSubmit fires a submit event with the given action and form data.
func (h *ComponentHarness[C]) SendSubmit(action string, data map[string]string) {
	h.SendEvent(tether.Event{
		Type:   event.Submit,
		Action: action,
		Data:   data,
	})
}

// SendEvent fires an arbitrary event and captures the response.
func (h *ComponentHarness[C]) SendEvent(ev tether.Event) {
	cs := &tether.CaptureSession{SessionID: "tethertest-component"}
	h.comp = h.comp.Handle(cs, ev).(C)
	h.last = cs.Effects
}

// Mount triggers the [tether.Mounter] lifecycle callback. Panics if
// the component does not implement [tether.Mounter].
func (h *ComponentHarness[C]) Mount() {
	m, ok := any(h.comp).(tether.Mounter)
	if !ok {
		panic("tethertest: Mount called but component does not implement tether.Mounter")
	}
	cs := &tether.CaptureSession{SessionID: "tethertest-component"}
	h.comp = m.Mount(cs).(C)
	h.last = cs.Effects
}

// Component returns the current component value.
func (h *ComponentHarness[C]) Component() C { return h.comp }

// HTML returns the component's rendered HTML.
func (h *ComponentHarness[C]) HTML() string { return string(h.comp.Render().Render()) }

// Toast returns the toast from the most recent event.
func (h *ComponentHarness[C]) Toast() string { return h.last.Toast }

// HasToast reports whether the most recent event triggered a matching toast.
func (h *ComponentHarness[C]) HasToast(text string) bool { return h.last.Toast == text }

// URL returns the navigation URL from the most recent event.
func (h *ComponentHarness[C]) URL() string { return h.last.URL }

// Replaced reports whether the most recent URL change used
// ReplaceURL rather than Navigate.
func (h *ComponentHarness[C]) Replaced() bool { return h.last.Replace }

// Title returns the title from the most recent event.
func (h *ComponentHarness[C]) Title() string { return h.last.Title }

// Announce returns the accessibility announcement from the most recent event.
func (h *ComponentHarness[C]) Announce() string { return h.last.Announce }

// HasAnnounce reports whether the most recent event triggered a matching announcement.
func (h *ComponentHarness[C]) HasAnnounce(text string) bool { return h.last.Announce == text }

// Flash returns the flash messages from the most recent event.
func (h *ComponentHarness[C]) Flash() map[string]string { return h.last.Flash }

// HasFlash reports whether the most recent event triggered a matching flash.
func (h *ComponentHarness[C]) HasFlash(selector, text string) bool {
	return h.last.Flash != nil && h.last.Flash[selector] == text
}

// Signals returns the signal values from the most recent event.
func (h *ComponentHarness[C]) Signals() map[string]any { return h.last.Signals }

// HasSignal reports whether the most recent event pushed a matching signal.
func (h *ComponentHarness[C]) HasSignal(key string, value any) bool {
	v, ok := h.last.Signals[key]
	return ok && v == value
}
