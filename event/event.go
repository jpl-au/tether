// Package event defines the event types that the client JS sends to
// the server. Use these constants instead of raw strings for type
// safety when constructing or comparing [tether.Event] values.
//
//	if ev.Type == event.Navigate { ... }
//	tether.Event{Type: event.Click, Action: "save"}
package event

// Type is the DOM event name carried by a [tether.Event]. The client JS
// serialises the originating DOM event's type into this field.
type Type string

// The constants below name the events with a predefined [bind] option,
// plus the synthetic ones the client raises with no DOM event behind
// them. They are a convenience, not a closed set: Type is a defined
// string type, so any event bound with [bind.On] compares directly.
//
//	if ev.Type == event.Click { ... }
//	if ev.Type == "wheel" { ... }

const (
	// Click is a mouse click on an element with a data-tether-click
	// attribute.
	Click Type = "click"

	// Input fires on every keystroke in an input, textarea, or
	// contenteditable element with a data-tether-input attribute.
	Input Type = "input"

	// Submit fires when a form with a data-tether-submit attribute is
	// submitted.
	Submit Type = "submit"

	// Change fires when a select, checkbox, or radio with a
	// data-tether-change attribute changes value.
	Change Type = "change"

	// KeyDown fires on keypress for elements with a data-tether-keydown
	// attribute. The key name is in Data["key"].
	KeyDown Type = "keydown"

	// Focus fires when an element with a data-tether-focus attribute
	// receives focus.
	Focus Type = "focus"

	// Blur fires when an element with a data-tether-blur attribute
	// loses focus.
	Blur Type = "blur"

	// Navigate is sent by the client when the user clicks a tether link
	// or uses the browser back/forward buttons. Data carries "path"
	// and "search" keys.
	Navigate Type = "navigate"

	// Paste fires when the user pastes content into an element with a
	// data-tether-paste attribute. The pasted text is in Data["value"].
	Paste Type = "paste"

	// Viewport fires when a bind.OnViewport-marked sentinel element
	// enters the visible viewport. Use this to implement infinite
	// scroll: load the next page of data when the sentinel is reached.
	Viewport Type = "viewport"

	// Online fires when the browser transitions from offline to online.
	// Use this to resume background tasks or dismiss a disconnection banner.
	Online Type = "online"

	// Offline fires when the browser loses network connectivity.
	// Use this to pause background tasks or show a disconnection banner.
	Offline Type = "offline"

	// AppInstalled fires when the user installs the PWA via the browser
	// prompt or an in-app install button. Use this to update UI that
	// offered the install prompt.
	AppInstalled Type = "appinstalled"
)
