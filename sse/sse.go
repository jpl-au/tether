// Package sse provides an SSE transport for tether. Use it when
// the deployment environment does not support WebSocket (e.g. certain
// PaaS providers, corporate proxies, or HTTP/2-only setups).
//
// The transport is unidirectional — server→client only. Updates flow
// as Server-Sent Events over a long-lived HTTP GET (EventSource on
// the client side). Client events arrive as individual HTTP POST
// requests and are routed directly to the session's command channel
// by the tether handler.
//
// Wire up by passing sse.Upgrade() as the Fallback (or Upgrade) field
// in [tether.Config] and setting Mode to [mode.ServerSentEvents] or
// [mode.Both].
package sse

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	tether "github.com/jpl-au/tether"
)

// heartbeatMsg is the SSE comment written by the heartbeat ticker.
// Allocated once and shared across all transports — read-only.
var heartbeatMsg = []byte(": heartbeat\n\n")

// Upgrade returns an upgrade function for use in [tether.Config].Fallback
// (or Upgrade when Mode is mode.ServerSentEvents). When the tether handler receives a
// GET with Accept: text/event-stream, it calls this function to
// establish the SSE stream. The stream stays open for the lifetime of
// the session; server updates are written as SSE "data" lines.
func Upgrade() func(http.ResponseWriter, *http.Request) (tether.Transport, error) {
	return func(w http.ResponseWriter, r *http.Request) (tether.Transport, error) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Set the EventSource reconnection interval so the browser
		// retries promptly if the stream drops before our JS loads.
		if _, err := fmt.Fprintf(w, "retry: 1000\n\n"); err != nil {
			return nil, fmt.Errorf("SSE handshake write failed: %w", err)
		}
		flusher.Flush()

		t := &transport{
			writes: make(chan []byte, 4),
			done:   make(chan struct{}),
		}

		go t.writeLoop(w, flusher)

		// Close when the HTTP connection drops so ReceiveEvent
		// returns io.EOF and the session event loop exits.
		go func() {
			<-r.Context().Done()
			t.Close()
		}()

		return t, nil
	}
}

// transport implements [tether.Transport] using SSE for the server→client
// direction. A dedicated writer goroutine owns the http.ResponseWriter
// — Send and StartHeartbeat submit payloads to the writes channel, and
// the writer serialises them onto the wire.
//
// ReceiveEvent blocks until the transport is closed. It returns the
// write error that caused the closure (if any) so the session can
// distinguish clean disconnects from broken pipes. Client events in
// SSE mode arrive as HTTP POSTs and are routed directly to the
// session's command channel — they never pass through the transport.
type transport struct {
	writes chan []byte
	done   chan struct{}
	once   sync.Once
	err    error
}

// writeLoop owns the http.ResponseWriter. It reads framed payloads
// from the writes channel and flushes each one immediately. If a write
// fails the transport is closed, causing ReceiveEvent to return io.EOF
// and the session loop to trigger the normal disconnect flow.
func (t *transport) writeLoop(w io.Writer, flusher http.Flusher) {
	for {
		select {
		case data := <-t.writes:
			if _, err := w.Write(data); err != nil {
				t.closeWithErr(err)
				return
			}
			flusher.Flush()
		case <-t.done:
			return
		}
	}
}

// Send submits pre-encoded JSON bytes to the writer goroutine, framed
// as an SSE "data" line followed by the message delimiter (double
// newline). Returns io.EOF if the transport is already closed.
func (t *transport) Send(data []byte) error {
	// Fast path: if the transport is already closed, return immediately.
	// Without this, the select below can non-deterministically choose the
	// writes case (buffered channel has space) even though done is closed.
	select {
	case <-t.done:
		return io.EOF
	default:
	}
	msg := fmt.Appendf(nil, "data: %s\n\n", data)
	select {
	case t.writes <- msg:
		return nil
	case <-t.done:
		return io.EOF
	}
}

// ReceiveEvent blocks until the transport is closed, then returns
// io.EOF. In SSE mode, client events arrive as HTTP POSTs and are
// routed directly to the session's command channel — they never pass
// through this method. The session's readTransport goroutine calls
// ReceiveEvent in a loop; it exits when Close is called (HTTP
// connection drop or session shutdown).
func (t *transport) ReceiveEvent() (tether.Event, error) {
	<-t.done
	if t.err != nil {
		return tether.Event{}, t.err
	}
	return tether.Event{}, io.EOF
}

// StartHeartbeat sends SSE comment lines at the given interval to
// prevent intermediate proxies from closing idle connections. SSE
// comments (lines starting with `:`) are silently discarded by the
// EventSource client and cost almost nothing on the wire.
//
// Heartbeat payloads are submitted to the writer goroutine's channel,
// so no additional synchronisation is needed.
func (t *transport) StartHeartbeat(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case t.writes <- heartbeatMsg:
				case <-t.done:
					return
				}
			case <-t.done:
				return
			}
		}
	}()
}

// closeWithErr records the terminal error and closes the transport.
// Safe to call from any goroutine; only the first call takes effect.
func (t *transport) closeWithErr(err error) {
	t.once.Do(func() {
		t.err = err
		close(t.done)
	})
}

// Close terminates the transport. Safe to call from any goroutine and
// safe to call more than once.
func (t *transport) Close() error {
	t.closeWithErr(io.EOF)
	return nil
}
