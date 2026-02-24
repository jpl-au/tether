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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	poly "github.com/jpl-au/fluent-poly"
)

// Upgrade returns an upgrade function for use in [poly.Config].Fallback
// (or Upgrade when Mode is SSEOnly). When the poly handler receives a
// GET with Accept: text/event-stream, it calls this function to
// establish the SSE stream. The stream stays open for the lifetime of
// the session; server updates are written as SSE "data" lines. Client
// events arrive separately via HTTP POST — the poly handler routes
// them through the [poly.EventPusher] interface on this transport.
func Upgrade() func(http.ResponseWriter, *http.Request) (poly.Transport, error) {
	return func(w http.ResponseWriter, r *http.Request) (poly.Transport, error) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		t := &transport{
			w:       w,
			flusher: flusher,
			events:  make(chan poly.Event, 16),
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
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	t.wmu.Lock()
	_, err = fmt.Fprintf(t.w, "data: %s\n\n", data)
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
// not consuming events fast enough and the internal buffer (capacity 16)
// is full, PushEvent returns [poly.ErrEventBufferFull] immediately so
// the HTTP handler can respond with 429 rather than stalling the
// request goroutine.
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

// Close terminates the transport. Safe to call from any goroutine and
// safe to call more than once.
func (t *transport) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}
