package sse

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// mockWriter implements http.ResponseWriter and http.Flusher backed
// by a bytes.Buffer so we can inspect SSE output without a real HTTP
// connection. The onFlush channel signals after each Flush so tests
// can synchronise with the asynchronous writer goroutine.
type mockWriter struct {
	buf     bytes.Buffer
	headers http.Header
	status  int
	flushed int
	onFlush chan struct{}
}

func newMockWriter() *mockWriter {
	return &mockWriter{
		headers: make(http.Header),
		onFlush: make(chan struct{}, 8),
	}
}

func (w *mockWriter) Header() http.Header         { return w.headers }
func (w *mockWriter) WriteHeader(statusCode int)  { w.status = statusCode }
func (w *mockWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *mockWriter) Flush() {
	w.flushed++
	if w.onFlush != nil {
		w.onFlush <- struct{}{}
	}
}

func newTestTransport() (*transport, *mockWriter) {
	w := newMockWriter()
	t := &transport{
		writes: make(chan []byte, 4),
		done:   make(chan struct{}),
	}
	go t.writeLoop(w, w)
	return t, w
}

func TestSendWritesSSEFormat(t *testing.T) {
	tr, w := newTestTransport()
	defer tr.Close()

	data := []byte(`{"type":"update","patches":[{"key":"count","html":"<span>1</span>"}]}`)
	if err := tr.Send(data); err != nil {
		t.Fatalf("Send error: %v", err)
	}

	// Wait for the writer goroutine to flush.
	<-w.onFlush

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
		t.Error("expected Flush to be called after Send")
	}
}

func TestReceiveEventReturnsEOFOnClose(t *testing.T) {
	tr, _ := newTestTransport()
	tr.Close()

	_, err := tr.ReceiveEvent()
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestSendReturnsEOFWhenClosed(t *testing.T) {
	tr, _ := newTestTransport()
	tr.Close()

	err := tr.Send([]byte(`{"type":"update"}`))
	if !errors.Is(err, io.EOF) {
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
