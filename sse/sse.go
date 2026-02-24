// Package sse provides an SSE+POST transport for fluent-poly.
//
// Server-to-client updates are sent as Server-Sent Events over a
// long-lived HTTP GET. Client-to-server events arrive as HTTP POST
// requests and are routed to the transport's event channel by the
// poly handler via the EventPusher interface.
//
// Pass sse.Upgrade() as the Fallback field in poly.Config and set Mode
// to SSEOnly or WebSocketWithFallback.
package sse

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	poly "github.com/jpl-au/fluent-poly"
)

// Upgrade returns an upgrade function for use in poly.Config.Fallback.
// When the handler receives a GET with Accept: text/event-stream, it
// calls this function to establish the SSE stream. Client events arrive
// separately via HTTP POST and are pushed through the EventPusher
// interface.
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

// transport implements poly.Transport over SSE (server→client) and a
// channel fed by HTTP POST (client→server).
type transport struct {
	w       http.ResponseWriter
	flusher http.Flusher
	events  chan poly.Event
	done    chan struct{}
	once    sync.Once
}

// SendUpdate encodes the update as JSON and writes it as an SSE data line.
func (t *transport) SendUpdate(update poly.Update) error {
	msg := poly.EncodeUpdate(update)
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(t.w, "data: %s\n\n", data)
	if err != nil {
		return err
	}
	t.flusher.Flush()
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

// PushEvent implements poly.EventPusher. The handler calls this when
// a POST request arrives for this session's event channel.
func (t *transport) PushEvent(ev poly.Event) error {
	// Check for closure first so a closed transport always rejects
	// new events, even when the buffered channel has space.
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
	}
}

// Close terminates the transport. Safe to call from any goroutine and
// safe to call more than once.
func (t *transport) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}
