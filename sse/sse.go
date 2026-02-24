// Package sse provides an SSE+POST transport for fluent-poly. Use it
// when the deployment environment does not support WebSocket (e.g.
// certain PaaS providers, corporate proxies, or HTTP/2-only setups).
//
// The transport splits the connection into two channels:
//   - Server→client: updates flow as Server-Sent Events over a
//     long-lived HTTP GET (EventSource on the client side).
//   - Client→server: events arrive as individual HTTP POST requests
//     and are routed to the transport's internal channel via the
//     [poly.EventPusher] interface.
//
// Wire up by passing sse.Upgrade() as the Fallback (or Upgrade) field
// in [poly.Config] and setting Mode to [poly.SSEOnly] or
// [poly.WebSocketWithFallback].
package sse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	poly "github.com/jpl-au/fluent-poly"
)

// defaultBufferSize is the event channel capacity when no explicit
// size is passed to Upgrade.
const defaultBufferSize = 16

// Upgrade returns an upgrade function for use in [poly.Config].Fallback
// (or Upgrade when Mode is SSEOnly). When the poly handler receives a
// GET with Accept: text/event-stream, it calls this function to
// establish the SSE stream. The stream stays open for the lifetime of
// the session; server updates are written as SSE "data" lines. Client
// events arrive separately via HTTP POST — the poly handler routes
// them through the [poly.EventPusher] interface on this transport.
//
// An optional bufferSize parameter sets the capacity of the internal
// event channel. When the channel is full, PushEvent returns
// [poly.ErrEventBufferFull] so the HTTP handler can respond with 429
// rather than blocking. The default is 16, which is sufficient for
// typical form-driven UIs. Increase it for high-frequency event
// streams such as mouse tracking or real-time collaboration.
func Upgrade(bufferSize ...int) func(http.ResponseWriter, *http.Request) (poly.Transport, error) {
	size := defaultBufferSize
	if len(bufferSize) > 0 && bufferSize[0] > 0 {
		size = bufferSize[0]
	}

	return func(w http.ResponseWriter, r *http.Request) (poly.Transport, error) {
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
		fmt.Fprintf(w, "retry: 1000\n\n")
		flusher.Flush()

		t := &transport{
			w:       w,
			flusher: flusher,
			events:  make(chan poly.Event, size),
			done:    make(chan struct{}),
		}

		// Close when the HTTP connection drops so ReceiveEvent
		// returns io.EOF and the session event loop exits.
		go func() {
			<-r.Context().Done()
			t.Close()
		}()

		return t, nil
	}
}

// transport implements [poly.Transport] using SSE for the server→client
// direction and a buffered channel for the client→server direction.
// The channel is fed by PushEvent, which the poly handler calls when
// an HTTP POST arrives for this session.
type transport struct {
	w       http.ResponseWriter
	flusher http.Flusher
	events  chan poly.Event
	done    chan struct{}
	once    sync.Once
	// wmu serialises writes to w. Today all SendUpdate calls are
	// serialised by the session mutex, but this guard protects against
	// future callers that might not hold that lock.
	wmu sync.Mutex
}

// SendUpdate encodes the update as JSON and writes it as an SSE "data"
// line followed by a double newline (the SSE message delimiter). The
// write is immediately flushed so the client receives it without
// buffering delay.
func (t *transport) SendUpdate(update poly.Update) error {
	msg := poly.EncodeUpdate(update)

	// Avoid json.Marshal's default HTML escaping (< to \u003c) which
	// inflates the size of DOM patches sent over the wire.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(msg); err != nil {
		return err
	}

	t.wmu.Lock()
	// Encode appends a newline, so we only need the SSE prefix and
	// one trailing newline for the double-newline message delimiter.
	_, err := fmt.Fprintf(t.w, "data: %s\n", buf.Bytes())
	if err != nil {
		t.wmu.Unlock()
		return err
	}
	t.flusher.Flush()
	t.wmu.Unlock()
	return nil
}

// ReceiveEvent blocks until an event is pushed via PushEvent or the
// transport is closed. Returns io.EOF when closed.
func (t *transport) ReceiveEvent() (poly.Event, error) {
	select {
	case ev := <-t.events:
		return ev, nil
	case <-t.done:
		return poly.Event{}, io.EOF
	}
}

// PushEvent implements [poly.EventPusher]. The poly handler calls this
// when an HTTP POST arrives carrying a client event for this session.
//
// The send is non-blocking by design. If the session's event loop is
// not consuming events fast enough and the internal buffer is full,
// PushEvent returns [poly.ErrEventBufferFull] immediately so the HTTP
// handler can respond with 429 rather than stalling the request
// goroutine. The buffer capacity is set via the bufferSize parameter
// to [Upgrade] (default 16).
func (t *transport) PushEvent(ev poly.Event) error {
	select {
	case <-t.done:
		return io.EOF
	default:
	}
	select {
	case t.events <- ev:
		return nil
	case <-t.done:
		return io.EOF
	default:
		return poly.ErrEventBufferFull
	}
}

// StartHeartbeat sends SSE comment lines at the given interval to
// prevent intermediate proxies from closing idle connections. SSE
// comments (lines starting with `:`) are silently discarded by the
// EventSource client and cost almost nothing on the wire.
func (t *transport) StartHeartbeat(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.wmu.Lock()
				_, err := fmt.Fprintf(t.w, ": heartbeat\n\n")
				if err != nil {
					t.wmu.Unlock()
					t.Close()
					return
				}
				t.flusher.Flush()
				t.wmu.Unlock()
			case <-t.done:
				return
			}
		}
	}()
}

// Close terminates the transport. Safe to call from any goroutine and
// safe to call more than once.
func (t *transport) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}
