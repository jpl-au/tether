// Package ws provides a WebSocket transport for fluent-poly. WebSocket
// gives full-duplex communication over a single TCP connection, so both
// server updates and client events travel on the same channel with
// minimal overhead. This is the default and preferred transport.
//
// Pass ws.Upgrade() as the Upgrade field in [poly.Config]. Origin
// checking is handled by the poly handler via [poly.Config].AllowedOrigins
// rather than by the websocket library directly.
package ws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/coder/websocket"
	poly "github.com/jpl-au/fluent-poly"
)

// Options configures the WebSocket transport.
type Options struct {
	// ReadLimit sets the maximum message size in bytes that the server
	// will accept from a client. Messages exceeding this limit cause
	// the connection to be closed with a protocol error. When zero,
	// the library default (32 KB) is used. Set this to match
	// [poly.Config].MaxEventBytes for consistent limits across
	// transport modes.
	ReadLimit int64
}

// Upgrade returns an upgrade function for use in [poly.Config].Upgrade.
// The returned function is called by the poly handler when it receives
// a WebSocket upgrade request. It negotiates the WebSocket handshake
// and returns a Transport that the session uses for its entire lifetime.
//
// Origin checking is handled by the poly handler via
// [poly.Config].AllowedOrigins, so Upgrade skips the websocket
// library's own origin verification to avoid double-checking.
func Upgrade(opts ...Options) func(http.ResponseWriter, *http.Request) (poly.Transport, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	return func(w http.ResponseWriter, r *http.Request) (poly.Transport, error) {
		// Origin checking is done by the poly handler before this
		// function is called, so skip the websocket library's check.
		acceptOpts := &websocket.AcceptOptions{InsecureSkipVerify: true}

		conn, err := websocket.Accept(w, r, acceptOpts)
		if err != nil {
			return nil, err
		}
		if o.ReadLimit > 0 {
			conn.SetReadLimit(o.ReadLimit)
		}
		return &transport{conn: conn, ctx: r.Context()}, nil
	}
}

// transport implements [poly.Transport] over a single WebSocket
// connection. The connection is owned by the session event loop for
// reads; writes are serialised by the underlying websocket library.
type transport struct {
	conn *websocket.Conn
	ctx  context.Context
}

// Send writes pre-encoded JSON bytes as a WebSocket text message. The
// coder/websocket library serialises concurrent writes, so this is
// safe to call from [Session.Update] goroutines.
func (t *transport) Send(data []byte) error {
	return t.conn.Write(t.ctx, websocket.MessageText, data)
}

// ReceiveEvent blocks until the client sends a JSON event message.
// Normal closure (1000) and going-away (1001) status codes are mapped
// to io.EOF so the session event loop treats them as clean disconnects
// rather than errors. All other WebSocket errors propagate as-is and
// will terminate the session.
func (t *transport) ReceiveEvent() (poly.Event, error) {
	_, data, err := t.conn.Read(t.ctx)
	if err != nil {
		// Map WebSocket close to io.EOF so the session event loop
		// treats it as a clean disconnect.
		status := websocket.CloseStatus(err)
		if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
			return poly.Event{}, io.EOF
		}
		return poly.Event{}, err
	}

	var ev poly.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return poly.Event{}, err
	}
	return ev, nil
}

// Close sends a normal closure frame and terminates the connection.
func (t *transport) Close() error {
	return t.conn.Close(websocket.StatusNormalClosure, "session closed")
}
