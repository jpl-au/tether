package poly

// Event represents a client-side DOM event sent to the server.
// Type is the DOM event name (e.g. "click", "input", "submit").
// Action is the value from the data-poly-* attribute that triggered the event.
// Data carries event-specific values — for input events this includes the
// element's value, for submit events it includes all form field values.
type Event struct {
	Type   string            `json:"type"`
	Action string            `json:"action"`
	Data   map[string]string `json:"data,omitempty"`
}
