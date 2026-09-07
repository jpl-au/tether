package sse

import (
	"errors"
	"io"
	"net/http/httptest"
	"testing"
)

type handshakeCompressor struct {
	fail   string
	closed int
}

func (c *handshakeCompressor) Write(p []byte) (int, error) {
	if c.fail == "write" {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}
func (c *handshakeCompressor) Flush() error {
	if c.fail == "compressor flush" {
		return io.ErrClosedPipe
	}
	return nil
}
func (c *handshakeCompressor) Close() error { c.closed++; return nil }

type handshakeWriter struct {
	*httptest.ResponseRecorder
	fail bool
}

func (w handshakeWriter) FlushError() error {
	if w.fail {
		return io.ErrClosedPipe
	}
	return nil
}

func TestHandshakeFailureClosesCompressor(t *testing.T) {
	for _, stage := range []string{"write", "compressor flush", "response flush"} {
		t.Run(stage, func(t *testing.T) {
			comp := &handshakeCompressor{fail: stage}
			old := candidates
			candidates = []candidate{{"test", func(io.Writer, CompressionLevel) (compressor, error) { return comp, nil }}}
			t.Cleanup(func() { candidates = old })
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Accept-Encoding", "test")
			tr, err := Upgrade()(handshakeWriter{httptest.NewRecorder(), stage == "response flush"}, r)
			if tr != nil || !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("unexpected handshake result: %v, %v", tr, err)
			}
			if comp.closed != 1 {
				t.Errorf("compressor closed %d times, want once", comp.closed)
			}
		})
	}
}
