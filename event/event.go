// Package event defines the event types that the client JS sends to
// the server. Use these constants instead of raw strings for type
// safety when constructing or comparing [poly.Event] values.
//
//	if ev.Type == event.Navigate { ... }
//	poly.Event{Type: event.Click, Action: "save"}
package event

// Type is the DOM event name carried by a [poly.Event]. The client JS
// serialises the originating DOM event's type into this field.
type Type string

// Custom creates a Type from a raw string. Use this for custom DOM
// events that are not covered by the predefined constants.
//
//	poly.Event{Type: event.Custom("mouseover"), Action: "hover"}
func Custom(name string) Type { return Type(name) }

const (
	// Click is a mouse click on an element with a data-poly-click
	// attribute.
	Click Type = "click"

	// Input fires on every keystroke in an input, textarea, or
	// contenteditable element with a data-poly-input attribute.
	Input Type = "input"

	// Submit fires when a form with a data-poly-submit attribute is
	// submitted.
	Submit Type = "submit"

	// Change fires when a select, checkbox, or radio with a
	// data-poly-change attribute changes value.
	Change Type = "change"

	// KeyDown fires on keypress for elements with a data-poly-keydown
	// attribute. The key name is in Data["key"].
	KeyDown Type = "keydown"

	// Focus fires when an element with a data-poly-focus attribute
	// receives focus.
	Focus Type = "focus"

	// Blur fires when an element with a data-poly-blur attribute
	// loses focus.
	Blur Type = "blur"

	// Navigate is sent by the client when the user clicks a poly link
	// or uses the browser back/forward buttons. Data carries "path"
	// and "search" keys.
	Navigate Type = "navigate"
)
