// Package mode selects the wire protocol between server and browser.
// Pass one of the constants to [tether.Config].Mode.
//
//	tether.Config[State]{
//	    Mode: mode.Both,
//	    // ...
//	}
package mode

// Transport selects the wire protocol between server and browser.
// [WebSocket] gives bidirectional communication over a single
// connection. [ServerSentEvents] splits the channel: server→client
// updates flow over a long-lived EventSource stream, and client→server
// events arrive as individual HTTP POSTs. [Both] tries WebSocket first
// and falls back to SSE+POST automatically. [HTTP] is plain
// request/response with no persistent connection, used by [tether.Page].
type Transport int

const (
	// HTTP uses plain request/response — no persistent connection.
	// Client events are sent as individual POST requests and the
	// response carries the update. Used internally by [tether.Page].
	HTTP Transport = iota + 1

	// WebSocket accepts only WebSocket connections. The Fallback
	// field is ignored; Upgrade must be set.
	WebSocket

	// ServerSentEvents accepts only SSE+POST connections. Use this
	// when the deployment environment does not support WebSocket
	// (e.g. certain PaaS providers or corporate proxies). The
	// Upgrade field is ignored; Fallback must be set.
	ServerSentEvents

	// Both tries WebSocket first. If the client cannot establish a
	// WebSocket connection (e.g. the proxy strips the Upgrade
	// header), it falls back to SSE+POST automatically. Both
	// Upgrade and Fallback must be set.
	Both
)
