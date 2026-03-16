package sse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
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

// --- Upgrade tests ---

func TestUpgradeSetsHeaders(t *testing.T) {
	upgradeFn := Upgrade()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	tp, err := upgradeFn(w, r)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	defer tp.Close()

	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	// Connection: keep-alive is no longer set — it is redundant in
	// HTTP/1.1 (default behaviour) and invalid in HTTP/2.
	if got := w.Header().Get("Connection"); got != "" {
		t.Errorf("Connection = %q, want empty (not set)", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "retry: 1000") {
		t.Errorf("expected retry preamble, got:\n%s", body)
	}
}

// noFlushWriter implements http.ResponseWriter but not http.Flusher.
type noFlushWriter struct{ httptest.ResponseRecorder }

func (noFlushWriter) Flush() {} // not on the interface — just to satisfy write

func TestUpgradeRejectsNonFlusher(t *testing.T) {
	upgradeFn := Upgrade()
	w := &struct{ http.ResponseWriter }{httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	_, err := upgradeFn(w, r)
	if err == nil {
		t.Fatal("expected error for non-flusher writer")
	}
	if !strings.Contains(err.Error(), "flushing") {
		t.Errorf("error = %q, want mention of flushing", err)
	}
}

func TestUpgradeContextCancelCloses(t *testing.T) {
	upgradeFn := Upgrade()
	w := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	tp, err := upgradeFn(w, r)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	cancel()

	// ReceiveEvent should return once the context is cancelled.
	_, err = tp.ReceiveEvent()
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF after context cancel, got %v", err)
	}
}

// --- Multiple sends ---

func TestMultipleSendsOrdered(t *testing.T) {
	tr, w := newTestTransport()
	defer tr.Close()

	for i := range 3 {
		data := fmt.Appendf(nil, `{"n":%d}`, i)
		if err := tr.Send(data); err != nil {
			t.Fatalf("Send(%d): %v", i, err)
		}
		<-w.onFlush
	}

	output := w.buf.String()
	for i := range 3 {
		want := fmt.Sprintf(`data: {"n":%d}`, i)
		if !strings.Contains(output, want) {
			t.Errorf("missing payload %d in output:\n%s", i, output)
		}
	}
}

// --- ReceiveEvent returns write errors ---

// failWriter fails on the nth Write call.
type failWriter struct {
	n       int
	count   int
	headers http.Header
	flushCh chan struct{}
}

func newFailWriter(failAfter int) *failWriter {
	return &failWriter{
		n:       failAfter,
		headers: make(http.Header),
		flushCh: make(chan struct{}, 8),
	}
}

func (fw *failWriter) Header() http.Header        { return fw.headers }
func (fw *failWriter) WriteHeader(statusCode int) {}
func (fw *failWriter) Flush()                     { fw.flushCh <- struct{}{} }
func (fw *failWriter) Write(b []byte) (int, error) {
	fw.count++
	if fw.count >= fw.n {
		return 0, fmt.Errorf("write failed")
	}
	return len(b), nil
}

func TestReceiveEventReturnsWriteError(t *testing.T) {
	fw := newFailWriter(1) // fail on first write
	tr := &transport{
		writes: make(chan []byte, 4),
		done:   make(chan struct{}),
	}
	go tr.writeLoop(fw, fw)

	// Send triggers writeLoop to write, which will fail.
	tr.Send([]byte(`{"x":1}`))

	_, err := tr.ReceiveEvent()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, io.EOF) {
		t.Error("expected write error, got io.EOF")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Errorf("error = %q, want write failed", err)
	}
}

// --- Heartbeat tests ---

func TestStartHeartbeatWritesComments(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newMockWriter()
		tr := &transport{
			writes: make(chan []byte, 4),
			done:   make(chan struct{}),
		}
		go tr.writeLoop(w, w)

		tr.StartHeartbeat(5 * time.Second)

		// Advance past two heartbeat intervals.
		time.Sleep(11 * time.Second)

		tr.Close()
		synctest.Wait()

		output := w.buf.String()
		count := strings.Count(output, ": heartbeat\n\n")
		if count < 2 {
			t.Errorf("expected at least 2 heartbeat comments, got %d in:\n%s", count, output)
		}
	})
}

func TestStartHeartbeatStopsOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newMockWriter()
		tr := &transport{
			writes: make(chan []byte, 4),
			done:   make(chan struct{}),
		}
		go tr.writeLoop(w, w)

		tr.StartHeartbeat(5 * time.Second)
		tr.Close()
		synctest.Wait()

		before := w.buf.String()

		// Advance time — no more heartbeats should appear.
		time.Sleep(15 * time.Second)
		synctest.Wait()

		after := w.buf.String()
		if after != before {
			t.Errorf("heartbeat continued after Close:\nbefore: %q\nafter:  %q", before, after)
		}
	})
}
