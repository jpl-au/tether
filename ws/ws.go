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

// Upgrade returns an upgrade function for use in [poly.Config].Upgrade.
// The returned function is called by the poly handler when it receives
// a WebSocket upgrade request. It negotiates the WebSocket handshake
// and returns a Transport that the session uses for its entire lifetime.
//
// Origin checking is handled by the poly handler via
// [poly.Config].AllowedOrigins, so Upgrade skips the websocket
// library's own origin verification to avoid double-checking.
func Upgrade() func(http.ResponseWriter, *http.Request) (poly.Transport, error) {
	return func(w http.ResponseWriter, r *http.Request) (poly.Transport, error) {
		// Origin checking is done by the poly handler before this
		// function is called, so skip the websocket library's check.
		opts := &websocket.AcceptOptions{InsecureSkipVerify: true}

		conn, err := websocket.Accept(w, r, opts)
		if err != nil {
			return nil, err
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

// SendUpdate encodes the update as JSON and writes it as a WebSocket
// text message. The coder/websocket library serialises concurrent
// writes, so this is safe to call from [Session.Update] goroutines.
func (t *transport) SendUpdate(update poly.Update) error {
	msg := poly.EncodeUpdate(update)
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
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
