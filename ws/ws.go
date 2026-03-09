// Package ws provides a WebSocket transport for fluent-tether. WebSocket
// gives full-duplex communication over a single TCP connection, so both
// server updates and client events travel on the same channel with
// minimal overhead. This is the default and preferred transport.
//
// Pass ws.Upgrade() as the Upgrade field in [tether.Config]. Origin
// checking is handled by the tether handler via [tether.Config].AllowedOrigins
// rather than by the websocket library directly.
package ws

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	tether "github.com/jpl-au/fluent-tether"
	"github.com/lxzan/gws"
)

// Options configures the WebSocket transport.
type Options struct {
	// ReadLimit sets the maximum message size in bytes that the server
	// will accept from a client. Messages exceeding this limit cause
	// the connection to be closed with a protocol error. When zero,
	// the library default is used. Set this to match
	// [tether.Config].MaxEventBytes for consistent limits across
	// transport modes.
	ReadLimit int64
}

// Upgrade returns an upgrade function for use in [tether.Config].Upgrade.
// The returned function is called by the tether handler when it receives
// a WebSocket upgrade request. It negotiates the WebSocket handshake
// and returns a Transport that the session uses for its entire lifetime.
//
// Origin checking is handled by the tether handler via
// [tether.Config].AllowedOrigins, so the upgrader does not perform its
// own origin verification.
func Upgrade(opts ...Options) func(http.ResponseWriter, *http.Request) (tether.Transport, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	serverOpts := &gws.ServerOption{
		ParallelEnabled: false,
	}
	if o.ReadLimit > 0 {
		serverOpts.ReadMaxPayloadSize = int(o.ReadLimit)
	}

	upgrader := gws.NewUpgrader(&eventHandler{}, serverOpts)

	return func(w http.ResponseWriter, r *http.Request) (tether.Transport, error) {
		conn, err := upgrader.Upgrade(w, r)
		if err != nil {
			return nil, err
		}
		t := &transport{
			conn:   conn,
			events: make(chan tether.Event),
			done:   make(chan struct{}),
		}
		conn.Session().Store("t", t)
		go conn.ReadLoop()
		return t, nil
	}
}

// eventHandler implements [gws.Event] for the shared upgrader.
// Per-connection state is stored on the connection via
// SetSession/Session rather than on the handler itself.
type eventHandler struct {
	gws.BuiltinEventHandler
}

func sessionTransport(conn *gws.Conn) *transport {
	v, ok := conn.Session().Load("t")
	if !ok {
		return nil
	}
	return v.(*transport)
}

func (h *eventHandler) OnMessage(conn *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	t := sessionTransport(conn)
	if t == nil {
		return
	}

	var ev tether.Event
	if err := json.Unmarshal(msg.Bytes(), &ev); err != nil {
		t.closeWithErr(err)
		conn.WriteClose(1007, []byte("invalid payload"))
		return
	}

	select {
	case t.events <- ev:
	case <-t.done:
	}
}

func (h *eventHandler) OnClose(conn *gws.Conn, err error) {
	t := sessionTransport(conn)
	if t == nil {
		return
	}
	// Normal closure codes (1000 normal, 1001 going away) are expected
	// lifecycle events, not errors. Map them to io.EOF so the session's
	// readTransport treats them as a clean disconnect rather than logging
	// a spurious error. Genuine protocol errors propagate as-is.
	if err == nil || isNormalClose(err) {
		t.closeWithErr(io.EOF)
	} else {
		t.closeWithErr(err)
	}
}

// isNormalClose reports whether err represents a WebSocket closure that
// is part of normal operation (code 1000 or 1001). gws returns a
// structured error whose string contains the close code.
func isNormalClose(err error) bool {
	s := err.Error()
	return strings.Contains(s, "code=1000") || strings.Contains(s, "code=1001")
}

// transport implements [tether.Transport] over a single WebSocket
// connection. Reads are driven by gws's ReadLoop goroutine which
// delivers events via a channel. Writes are serialised by gws
// internally, so Send is safe to call from any goroutine.
type transport struct {
	conn   *gws.Conn
	events chan tether.Event
	done   chan struct{}
	err    error
	once   sync.Once
}

// closeWithErr records the terminal error and closes the events
// channel so that ReceiveEvent unblocks. Safe to call multiple times;
// only the first call takes effect.
func (t *transport) closeWithErr(err error) {
	t.once.Do(func() {
		t.err = err
		close(t.done)
		close(t.events)
	})
}

// Send writes pre-encoded JSON bytes as a WebSocket text message. gws
// serialises concurrent writes, so this is safe to call from
// [Session.Update] goroutines.
func (t *transport) Send(data []byte) error {
	return t.conn.WriteMessage(gws.OpcodeText, data)
}

// ReceiveEvent blocks until the client sends a JSON event message.
// Returns io.EOF when the connection is closed cleanly. All other
// errors propagate as-is and will terminate the session.
func (t *transport) ReceiveEvent() (tether.Event, error) {
	ev, ok := <-t.events
	if !ok {
		if t.err != nil {
			return tether.Event{}, t.err
		}
		return tether.Event{}, io.EOF
	}
	return ev, nil
}

// Close sends a normal closure frame and terminates the connection.
func (t *transport) Close() error {
	t.closeWithErr(io.EOF)
	return t.conn.WriteClose(1000, nil)
}
