package sse

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	poly "github.com/jpl-au/fluent-poly"
)

// mockWriter implements http.ResponseWriter and http.Flusher backed
// by a bytes.Buffer so we can inspect SSE output without a real HTTP
// connection.
type mockWriter struct {
	buf     bytes.Buffer
	headers http.Header
	status  int
	flushed int
}

func newMockWriter() *mockWriter {
	return &mockWriter{headers: make(http.Header)}
}

func (w *mockWriter) Header() http.Header         { return w.headers }
func (w *mockWriter) WriteHeader(statusCode int)   { w.status = statusCode }
func (w *mockWriter) Write(b []byte) (int, error)  { return w.buf.Write(b) }
func (w *mockWriter) Flush()                       { w.flushed++ }

func newTestTransport() (*transport, *mockWriter) {
	w := newMockWriter()
	t := &transport{
		w:       w,
		flusher: w,
		events:  make(chan poly.Event, 16),
		done:    make(chan struct{}),
	}
	return t, w
}

func TestSendUpdateWritesSSEFormat(t *testing.T) {
	tr, w := newTestTransport()

	update := poly.Update{
		Patches: []jit.Patch{{Key: "count", HTML: []byte("<span>1</span>")}},
	}

	if err := tr.SendUpdate(update); err != nil {
		t.Fatalf("SendUpdate error: %v", err)
	}

	output := w.buf.String()
	if !strings.HasPrefix(output, "data: ") {
		t.Errorf("expected SSE data prefix, got:\n%s", output)
	}
	if !strings.HasSuffix(output, "\n\n") {
		t.Errorf("expected double newline suffix, got:\n%q", output)
	}
	if !strings.Contains(output, `"type":"update"`) {
		t.Errorf("expected update type in output:\n%s", output)
	}
	if !strings.Contains(output, `"key":"count"`) {
		t.Errorf("expected patch key in output:\n%s", output)
	}
	if w.flushed < 1 {
		t.Error("expected Flush to be called after SendUpdate")
	}
}

func TestReceiveEventBlocksUntilPush(t *testing.T) {
	tr, _ := newTestTransport()

	want := poly.Event{Type: "click", Action: "increment"}
	go func() {
		tr.PushEvent(want)
	}()

	got, err := tr.ReceiveEvent()
	if err != nil {
		t.Fatalf("ReceiveEvent error: %v", err)
	}
	if got.Type != want.Type || got.Action != want.Action {
		t.Errorf("expected event {%s %s}, got {%s %s}", want.Type, want.Action, got.Type, got.Action)
	}
}

func TestReceiveEventReturnsEOFOnClose(t *testing.T) {
	tr, _ := newTestTransport()
	tr.Close()

	_, err := tr.ReceiveEvent()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestPushEventReturnsEOFWhenClosed(t *testing.T) {
	tr, _ := newTestTransport()
	tr.Close()

	err := tr.PushEvent(poly.Event{Type: "click", Action: "test"})
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	tr, _ := newTestTransport()

	if err := tr.Close(); err != nil {
		t.Errorf("first Close error: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Errorf("second Close error: %v", err)
	}
}
