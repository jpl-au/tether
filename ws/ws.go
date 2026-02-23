// Package ws provides a WebSocket transport for fluent-poly.
//
// Pass ws.Upgrade() as the Upgrade field in poly.Config.
package ws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/coder/websocket"
	poly "github.com/jpl-au/fluent-poly"
)

// Upgrade returns an upgrade function for use in poly.Config. When called
// with no arguments, all origins are accepted (suitable for development).
// Pass origin patterns to restrict connections in production.
//
//	Upgrade: ws.Upgrade(),                          // development
//	Upgrade: ws.Upgrade("https://example.com"),     // production
func Upgrade(origins ...string) func(http.ResponseWriter, *http.Request) (poly.Transport, error) {
	return func(w http.ResponseWriter, r *http.Request) (poly.Transport, error) {
		opts := &websocket.AcceptOptions{}
		if len(origins) == 0 {
			opts.InsecureSkipVerify = true
		} else {
			opts.OriginPatterns = origins
		}

		conn, err := websocket.Accept(w, r, opts)
		if err != nil {
			return nil, err
		}
		return &transport{conn: conn, ctx: r.Context()}, nil
	}
}

// transport implements poly.Transport over a single WebSocket connection.
type transport struct {
	conn *websocket.Conn
	ctx  context.Context
}

// SendUpdate encodes the update as JSON and writes it as a text message.
func (t *transport) SendUpdate(update poly.Update) error {
	msg := poly.EncodeUpdate(update)
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return t.conn.Write(t.ctx, websocket.MessageText, data)
}

// ReceiveEvent blocks until the client sends a JSON event message.
// Normal and going-away WebSocket closes are mapped to io.EOF so the
// session event loop treats them as clean disconnects.
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
