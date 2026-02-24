package poly

import "net/url"

// Event represents a client-side DOM event sent to the server.
// Type is the DOM event name (e.g. "click", "input", "submit").
// Action is the value from the data-poly-* attribute that triggered the event.
// Data carries event-specific values — for input events this includes the
// element's value, for submit events it includes all form field values.
// EventID is a client-generated correlation ID echoed back in the response
// so the client can match loading states to specific events.
type Event struct {
	Type    string            `json:"type"`
	Action  string            `json:"action"`
	Data    map[string]string `json:"data,omitempty"`
	EventID string            `json:"event_id,omitempty"`
}

// Params carries URL information from a navigation event. HandleParams
// receives this when the browser navigates (link click, back/forward,
// initial page load).
type Params struct {
	Path  string
	Query url.Values
}
