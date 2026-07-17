// Package sse provides an SSE transport for tether. Use it when
// the deployment environment does not support WebSocket (e.g. certain
// PaaS providers, corporate proxies, or HTTP/2-only setups).
//
// The transport is unidirectional - server→client only. Updates flow
// as Server-Sent Events over a long-lived HTTP GET (EventSource on
// the client side). Client events arrive as individual HTTP POST
// requests and are routed directly to the session's command channel
// by the tether handler.
//
// Wire up by passing sse.Upgrade() as the Fallback (or Upgrade) field
// in [tether.StatefulConfig] and setting Mode to [mode.ServerSentEvents] or
// [mode.Both].
//
// Responses are compressed by default (brotli, zstd, gzip, or deflate,
// negotiated against the client's Accept-Encoding). See [Compression].
package sse

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	xport "github.com/jpl-au/tether/internal/transport"
)

// defaultWriteBuffer is the capacity of the write channel when
// [Options].WriteBuffer is zero.
const defaultWriteBuffer = 4

// Options configures the SSE transport.
type Options struct {
	// WriteBuffer sets the capacity of the internal channel that
	// buffers encoded updates between the session's command loop and
	// the HTTP response writer. When the channel is full, Send blocks
	// until the writer drains it, stalling the session loop.
	//
	// Increase this for high-frequency update scenarios (live
	// dashboards, streaming data) where the client may fall a few
	// frames behind. The memory cost is small - each slot holds one
	// pre-encoded update (typically a few hundred bytes).
	//
	// Zero uses the default (4).
	WriteBuffer int

	// Compression configures response compression. The zero value
	// enables compression with sensible defaults (fastest level,
	// negotiated against Accept-Encoding). Set Compression.Disabled to
	// opt out.
	Compression Compression
}

// heartbeatMsg is the SSE comment written by the heartbeat ticker.
// Allocated once and shared across all transports - read-only.
var heartbeatMsg = []byte(": heartbeat\n\n")

// eventBufPool recycles the buffers used to frame each event. Each
// event is built in a single pooled buffer and written once, avoiding a
// fresh allocation per Send. The writer goroutine returns the buffer to
// the pool after writing it.
var eventBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// Upgrade returns an upgrade function for use in [tether.StatefulConfig].Fallback
// (or Upgrade when Mode is mode.ServerSentEvents). When the tether handler receives a
// GET with Accept: text/event-stream, it calls this function to
// establish the SSE stream. The stream stays open for the lifetime of
// the session; server updates are written as SSE "data" lines.
func Upgrade(opts ...Options) func(http.ResponseWriter, *http.Request) (xport.Transport, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	writeBuf := defaultWriteBuffer
	if o.WriteBuffer > 0 {
		writeBuf = o.WriteBuffer
	}

	compressEnabled := !o.Compression.Disabled
	level := o.Compression.Level
	if level == 0 {
		level = CompressionFastest
	}

	return func(w http.ResponseWriter, r *http.Request) (xport.Transport, error) {
		// SSE cannot function without flushing - every event must be
		// pushed to the client immediately. Check up front so a writer
		// that cannot flush fails with a clear error before we commit
		// any response headers, rather than stalling mid-stream.
		if !supportsFlush(w) {
			return nil, fmt.Errorf("sse: response writer does not support flushing")
		}

		// Disable write deadlines for the SSE stream. This ensures that
		// even if the server has a global WriteTimeout, it won't kill our
		// long-lived SSE connection. Not all ResponseWriters support this
		// - the error is non-fatal.
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("sse: SetWriteDeadline not supported", "error", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		// Connection: keep-alive is not set - it is invalid in HTTP/2
		// (RFC 7540 §8.1.2.2) and Go's HTTP/2 implementation strips
		// connection-specific headers. HTTP/1.1 keep-alive is the
		// default behaviour and does not need an explicit header.

		// Negotiate compression and wrap the writer before any body is
		// written. Content-Encoding must be set before WriteHeader.
		sw := &streamWriter{dst: w, rc: rc}
		if compressEnabled {
			if encoding, build := negotiate(r.Header.Get("Accept-Encoding"), level); build != nil {
				comp, err := build(w)
				if err != nil {
					return nil, fmt.Errorf("sse: build %s compressor: %w", encoding, err)
				}
				w.Header().Set("Content-Encoding", encoding)
				w.Header().Add("Vary", "Accept-Encoding")
				sw.dst = comp
				sw.comp = comp
			}
		}

		w.WriteHeader(http.StatusOK)

		// Set the EventSource reconnection interval so the browser
		// retries promptly if the stream drops before our JS loads. This
		// first write also proves the flush path end-to-end.
		if err := sw.writeFlush([]byte("retry: 1000\n\n")); err != nil {
			return nil, fmt.Errorf("sse: handshake write failed: %w", err)
		}

		t := &transport{
			writes:   make(chan *bytes.Buffer, writeBuf),
			done:     make(chan struct{}),
			finished: make(chan struct{}),
		}

		go t.writeLoop(sw)

		// Close when the HTTP connection drops so ReceiveEvent
		// returns io.EOF and the session event loop exits.
		go func() {
			<-r.Context().Done()
			t.Close()
		}()

		return t, nil
	}
}

// streamWriter frames-and-flushes SSE bytes all the way to the client.
// When compression is negotiated, dst is the compressor wrapped around
// the response writer and comp is that same compressor; for an
// uncompressed stream dst is the response writer and comp is nil.
type streamWriter struct {
	dst  io.Writer
	comp compressor
	rc   *http.ResponseController
}

// writeFlush writes one framed event and pushes it to the client.
// Compressors buffer internally, so the compressor is flushed first to
// drain its buffer into the response writer, then the response writer
// is flushed to put the bytes on the wire. Without both flushes the
// client sees nothing until the buffer happens to fill.
func (s *streamWriter) writeFlush(p []byte) error {
	if _, err := s.dst.Write(p); err != nil {
		return err
	}
	if s.comp != nil {
		if err := s.comp.Flush(); err != nil {
			return err
		}
	}
	return s.rc.Flush()
}

// Compile-time checks: *transport must satisfy xport.Transport
// and xport.Heartbeater (periodic keep-alive writes).
var (
	_ xport.Transport   = (*transport)(nil)
	_ xport.Heartbeater = (*transport)(nil)
)

// transport implements [xport.Transport] using SSE for the server→client
// direction. A dedicated writer goroutine owns the streamWriter - Send
// and StartHeartbeat submit framed buffers to the writes channel, and
// the writer serialises them onto the wire.
//
// ReceiveEvent blocks until the transport is closed. It returns the
// write error that caused the closure (if any) so the session can
// distinguish clean disconnects from broken pipes. Client events in
// SSE mode arrive as HTTP POSTs and are routed directly to the
// session's command channel - they never pass through the transport.
type transport struct {
	writes chan *bytes.Buffer
	done   chan struct{}
	// finished closes when writeLoop has returned and will no longer
	// touch the http.ResponseWriter. ReceiveEvent waits on it so the
	// tether handler - which returns once ReceiveEvent unblocks - never
	// returns while the writer goroutine is mid-flush. A ResponseWriter
	// must not be used after its handler returns, so this ordering is
	// what keeps SSE teardown race-free.
	finished chan struct{}
	once     sync.Once
	err      error
}

// writeLoop owns the streamWriter. It reads framed buffers from the
// writes channel, writes and flushes each one immediately, then returns
// the buffer to the pool. If a write fails the transport is closed,
// causing ReceiveEvent to return the error and the session loop to
// trigger the normal disconnect flow.
func (t *transport) writeLoop(sw *streamWriter) {
	// Signal that the writer goroutine has stopped touching the
	// ResponseWriter so ReceiveEvent (and thus the handler) can return
	// safely.
	defer close(t.finished)

	// Flush any trailing compressor state when the stream ends. Every
	// event already flushes, so this only matters if the loop exits
	// with bytes still buffered; the error is ignored because the
	// client is gone by this point.
	if sw.comp != nil {
		defer sw.comp.Close()
	}
	for {
		select {
		case buf := <-t.writes:
			err := sw.writeFlush(buf.Bytes())
			eventBufPool.Put(buf)
			if err != nil {
				t.closeWithErr(err)
				return
			}
		case <-t.done:
			return
		}
	}
}

// Send frames pre-encoded JSON bytes as an SSE "data" line followed by
// the message delimiter (double newline) and submits it to the writer
// goroutine. Returns io.EOF if the transport is already closed.
func (t *transport) Send(data []byte) error {
	// Fast path: if the transport is already closed, return immediately.
	// Without this, the select below can non-deterministically choose the
	// writes case (buffered channel has space) even though done is closed.
	select {
	case <-t.done:
		return io.EOF
	default:
	}

	buf := eventBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	select {
	case t.writes <- buf:
		return nil
	case <-t.done:
		// Never handed off - recycle it ourselves.
		eventBufPool.Put(buf)
		return io.EOF
	}
}

// ReceiveEvent blocks until the transport is closed, then returns
// io.EOF. In SSE mode, client events arrive as HTTP POSTs and are
// routed directly to the session's command channel - they never pass
// through this method. The session's readTransport goroutine calls
// ReceiveEvent in a loop; it exits when Close is called (HTTP
// connection drop or session shutdown).
func (t *transport) ReceiveEvent() (xport.Event, error) {
	<-t.done
	// Wait for the writer goroutine to finish with the ResponseWriter
	// before returning. The handler returns once this call unblocks, and
	// it must not do so while a flush is still in flight.
	<-t.finished
	if t.err != nil {
		return xport.Event{}, t.err
	}
	return xport.Event{}, io.EOF
}

// StartHeartbeat sends SSE comment lines at the given interval to
// prevent intermediate proxies from closing idle connections. SSE
// comments (lines starting with `:`) are silently discarded by the
// EventSource client and cost almost nothing on the wire. Like any
// other event, each heartbeat is flushed through the compressor and the
// response writer so it actually reaches the client.
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
				buf := eventBufPool.Get().(*bytes.Buffer)
				buf.Reset()
				buf.Write(heartbeatMsg)
				select {
				case t.writes <- buf:
				case <-t.done:
					eventBufPool.Put(buf)
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
