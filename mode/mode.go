// Package mode selects the wire protocol between server and browser.
// Pass one of the constants to [poly.Config].Mode.
//
//	poly.Config[State]{
//	    Mode: mode.Auto,
//	    // ...
//	}
package mode

// Transport selects the wire protocol between server and browser.
// WebSocket gives bidirectional communication over a single connection.
// SSE+POST splits the channel: server→client updates flow over a
// long-lived EventSource stream, and client→server events arrive as
// individual HTTP POSTs. SSE+POST works through HTTP/2 reverse proxies
// and load balancers that may not support WebSocket, at the cost of
// slightly higher latency on client events.
type Transport int

const (
	// WebSocket accepts only WebSocket connections. This is the
	// default when Mode is not set. The Fallback field is ignored.
	WebSocket Transport = iota

	// SSE accepts only SSE+POST connections. Use this when the
	// deployment environment does not support WebSocket (e.g.
	// certain PaaS providers or corporate proxies). The Upgrade
	// field is ignored; Fallback must be set.
	SSE

	// Auto tries WebSocket first. If the client cannot establish a
	// WebSocket connection (e.g. the proxy strips the Upgrade
	// header), it falls back to SSE+POST automatically. Both
	// Upgrade and Fallback must be set.
	Auto
)
