// Package ws provides a WebSocket transport for tether. WebSocket
// gives full-duplex communication over a single TCP connection, so both
// server updates and client events travel on the same channel with
// minimal overhead. This is the default and preferred transport.
//
// Pass ws.Upgrade() as the Upgrade field in [tether.StatefulConfig]. Origin
// checking is handled by the tether handler via [tether.StatefulConfig].TrustedOrigins
// rather than by the websocket library directly.
package ws

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	xport "github.com/jpl-au/tether/internal/transport"
	"github.com/lxzan/gws"
)

// CompressionLevel controls the deflate compression level for
// per-message compression. Higher levels produce smaller messages
// at the cost of more CPU. The zero value selects the default
// (fastest).
type CompressionLevel int

const (
	// CompressionFastest uses the least CPU per message. This is
	// the default and the best choice for real-time updates where
	// latency matters more than payload size.
	CompressionFastest CompressionLevel = 1

	// CompressionBalanced trades some CPU for better compression
	// ratios. Useful when bandwidth is more constrained than CPU.
	CompressionBalanced CompressionLevel = 6

	// CompressionSmallest produces the smallest possible messages
	// at the highest CPU cost. Rarely appropriate for real-time
	// traffic.
	CompressionSmallest CompressionLevel = 9
)

// Compression configures WebSocket per-message deflate (RFC 7692).
// Compression is enabled by default with sensible defaults - set
// Disabled to opt out. When enabled, the server negotiates the
// permessage-deflate extension during the WebSocket handshake.
// Browsers handle decompression transparently.
type Compression struct {
	// Disabled turns off per-message deflate. Use this when a
	// reverse proxy already handles compression, or for debugging.
	Disabled bool

	// Level sets the deflate compression level. Zero defaults to
	// [CompressionFastest], which is the best trade-off for
	// real-time updates.
	Level CompressionLevel

	// Threshold is the minimum message size in bytes before
	// compression is applied. Messages smaller than this are sent
	// uncompressed to avoid the overhead of deflating tiny
	// payloads. Zero defaults to 512 bytes.
	Threshold int

	// ContextTakeover enables per-connection compression context.
	// The compressor retains its sliding window across messages,
	// producing significantly better ratios for repetitive content
	// like HTML fragments. The cost is additional memory per
	// connection (~4 KB at the default window size) instead of a
	// fixed shared pool. When disabled (default), each message is
	// compressed independently using a shared pool of compressors.
	ContextTakeover bool
}

// Options configures the WebSocket transport.
type Options struct {
	// ReadLimit sets the maximum message size in bytes that the server
	// will accept from a client. Messages exceeding this limit cause
	// the connection to be closed with a protocol error. When zero,
	// the library default is used. Set this to match
	// [tether.StatefulConfig].MaxEventBytes for consistent limits across
	// transport modes.
	ReadLimit int64

	// Compression configures per-message deflate (RFC 7692).
	// The zero value enables compression with sensible defaults:
	// fastest compression level, 512-byte threshold, and a shared
	// compressor pool. Set Compression.Disabled to opt out.
	Compression Compression
}

// Upgrade returns an upgrade function for use in [tether.StatefulConfig].Upgrade.
// The returned function is called by the tether handler when it receives
// a WebSocket upgrade request. It negotiates the WebSocket handshake
// and returns a Transport that the session uses for its entire lifetime.
//
// Origin checking is handled by the tether handler via
// [tether.StatefulConfig].TrustedOrigins, so the upgrader does not perform its
// own origin verification.
func Upgrade(opts ...Options) func(http.ResponseWriter, *http.Request) (xport.Transport, error) {
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
	if !o.Compression.Disabled {
		serverOpts.PermessageDeflate = gws.PermessageDeflate{
			Enabled:               true,
			Level:                 int(o.Compression.Level),
			Threshold:             o.Compression.Threshold,
			ServerContextTakeover: o.Compression.ContextTakeover,
			ClientContextTakeover: o.Compression.ContextTakeover,
		}
	}

	upgrader := gws.NewUpgrader(&eventHandler{}, serverOpts)

	return func(w http.ResponseWriter, r *http.Request) (xport.Transport, error) {
		conn, err := upgrader.Upgrade(w, r)
		if err != nil {
			return nil, err
		}
		t := &transport{
			conn:   conn,
			events: make(chan xport.Event),
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

	var ev xport.Event
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

func (h *eventHandler) OnPong(conn *gws.Conn, _ []byte) {
	// Reset the read deadline so the connection stays alive as long
	// as the client is responding. The zero time removes the deadline
	// entirely; StartHeartbeat will set a fresh one on the next tick.
	conn.SetDeadline(time.Time{})
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

// Compile-time checks: *transport must satisfy xport.Transport
// and xport.Heartbeater (WebSocket ping/pong keep-alive).
var (
	_ xport.Transport   = (*transport)(nil)
	_ xport.Heartbeater = (*transport)(nil)
)

// transport implements [xport.Transport] over a single WebSocket
// connection. Reads are driven by gws's ReadLoop goroutine which
// delivers events via a channel. Writes are serialised by gws
// internally, so Send is safe to call from any goroutine.
type transport struct {
	conn   *gws.Conn
	events chan xport.Event
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
func (t *transport) ReceiveEvent() (xport.Event, error) {
	ev, ok := <-t.events
	if !ok {
		if t.err != nil {
			return xport.Event{}, t.err
		}
		return xport.Event{}, io.EOF
	}
	return ev, nil
}

// StartHeartbeat sends WebSocket ping frames at the given interval
// and sets a read deadline so that connections with no pong response
// are detected and closed. This prevents goroutine leaks when a
// middlebox silently drops the connection without sending a FIN.
//
// On each tick the transport sends a ping and sets a read deadline
// of 2x the interval. If the client responds with a pong, OnPong
// resets the deadline. If no pong arrives, the deadline fires, gws's
// ReadLoop returns an error, and the normal disconnect flow runs.
func (t *transport) StartHeartbeat(interval time.Duration) {
	deadline := 2 * interval
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.conn.SetDeadline(time.Now().Add(deadline))
				if err := t.conn.WritePing(nil); err != nil {
					return
				}
			case <-t.done:
				return
			}
		}
	}()
}

// Close sends a normal closure frame and terminates the connection.
func (t *transport) Close() error {
	t.closeWithErr(io.EOF)
	return t.conn.WriteClose(1000, nil)
}
