package sse

import (
	"bufio"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// --- Negotiation unit tests (pure functions, no HTTP) ---

func TestAcceptedEncodings(t *testing.T) {
	cases := []struct {
		header string
		want   []string // encodings expected in the set
		absent []string // encodings expected NOT in the set
	}{
		{"gzip, deflate, br", []string{"gzip", "deflate", "br"}, nil},
		{"br;q=1.0, gzip;q=0.8", []string{"br", "gzip"}, nil},
		{"gzip;q=0", nil, []string{"gzip"}},                // q=0 refuses gzip
		{"gzip;q=0, br", []string{"br"}, []string{"gzip"}}, // one refused, one kept
		{"  GZIP  ,  BR  ", []string{"gzip", "br"}, nil},   // case and whitespace
		{"", nil, []string{"gzip", "br"}},                  // empty header
		{"!!!;;;garbage,,,", nil, []string{"gzip", "br"}},  // junk is ignored, not fatal
		{"gzip;q=notanumber", []string{"gzip"}, nil},       // malformed q kept (no preference)
	}
	for _, c := range cases {
		got := acceptedEncodings(c.header)
		for _, enc := range c.want {
			if !got[enc] {
				t.Errorf("acceptedEncodings(%q) missing %q, got %v", c.header, enc, got)
			}
		}
		for _, enc := range c.absent {
			if got[enc] {
				t.Errorf("acceptedEncodings(%q) unexpectedly has %q, got %v", c.header, enc, got)
			}
		}
	}
}

func TestNegotiateServerPriority(t *testing.T) {
	// With everything on offer the server prefers brotli.
	if enc, build := negotiate("gzip, deflate, br, zstd", CompressionFastest); enc != "br" || build == nil {
		t.Errorf("full header: got %q, want br", enc)
	}
	// Only gzip accepted → gzip.
	if enc, build := negotiate("gzip", CompressionFastest); enc != "gzip" || build == nil {
		t.Errorf("gzip only: got %q, want gzip", enc)
	}
	// Nothing we support → identity (empty encoding, nil constructor).
	if enc, build := negotiate("identity, exotic-thing", CompressionFastest); enc != "" || build != nil {
		t.Errorf("unsupported: got %q (build!=nil: %v), want identity", enc, build != nil)
	}
	// Junk header → identity, no panic.
	if enc, build := negotiate("!!!garbage;;;", CompressionFastest); enc != "" || build != nil {
		t.Errorf("junk: got %q, want identity", enc)
	}
}

// --- Integration harness ---

// sseTestServer runs the SSE transport behind a real HTTP server so
// tests can exercise the full negotiate → compress → flush path with an
// ordinary http.Client. Events are pushed via send and streamed to the
// client by the handler.
type sseTestServer struct {
	srv    *httptest.Server
	events chan []byte
}

// startServer boots a server whose handler upgrades to SSE and streams
// whatever the test sends. wrap, if non-nil, decorates the
// ResponseWriter before Upgrade sees it (used to hide http.Flusher).
func startServer(t *testing.T, wrap func(http.ResponseWriter) http.ResponseWriter, opts ...Options) *sseTestServer {
	t.Helper()
	s := &sseTestServer{events: make(chan []byte)}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := w
		if wrap != nil {
			rw = wrap(w)
		}
		tp, err := Upgrade(opts...)(rw, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Relay test-supplied events until the client disconnects.
		go func() {
			for {
				select {
				case data := <-s.events:
					if tp.Send(data) != nil {
						return
					}
				case <-r.Context().Done():
					return
				}
			}
		}()
		// Mirror the framework's readTransport: block until the transport
		// is fully closed. ReceiveEvent only returns after the writer
		// goroutine has stopped using the ResponseWriter, so the handler
		// never returns mid-flush.
		tp.ReceiveEvent()
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sseTestServer) send(t *testing.T, data string) {
	t.Helper()
	select {
	case s.events <- []byte(data):
	case <-time.After(2 * time.Second):
		t.Fatal("timed out submitting event to handler")
	}
}

// open sends the GET, returning the response and a bufio.Reader over the
// decompressed body positioned at the first SSE frame.
func (s *sseTestServer) open(t *testing.T, acceptEncoding string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	// A dedicated client with keep-alives off so closing the body tears
	// the connection down and unblocks the handler.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	return resp, bufio.NewReader(decodeBody(t, resp))
}

// decodeBody wraps the response body in the reader matching its
// Content-Encoding. The Go client does not auto-decompress when the test
// sets Accept-Encoding itself, so we decompress explicitly and thereby
// assert the negotiated encoding really is what the server claims.
func decodeBody(t *testing.T, resp *http.Response) io.Reader {
	t.Helper()
	switch enc := resp.Header.Get("Content-Encoding"); enc {
	case "":
		return resp.Body
	case "gzip":
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		return gr
	case "br":
		return brotli.NewReader(resp.Body)
	case "zstd":
		zr, err := zstd.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("zstd reader: %v", err)
		}
		return zr
	default:
		t.Fatalf("unexpected Content-Encoding %q", enc)
		return nil
	}
}

// readFrame reads one SSE frame (lines up to the terminating blank line)
// from the decompressed stream.
func readFrame(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("readFrame: %v (partial %q)", err, b.String())
		}
		if line == "\n" {
			return b.String()
		}
		b.WriteString(line)
	}
}

// roundTrip drives one full negotiated stream: it opens with the given
// Accept-Encoding, checks the Content-Encoding, then sends two events
// and asserts they arrive framed and intact after decompression.
func roundTrip(t *testing.T, acceptEncoding, wantEncoding string) {
	t.Helper()
	s := startServer(t, nil)
	resp, br := s.open(t, acceptEncoding)

	if got := resp.Header.Get("Content-Encoding"); got != wantEncoding {
		t.Fatalf("Content-Encoding = %q, want %q", got, wantEncoding)
	}

	// First frame is the retry preamble written during the handshake.
	if pre := readFrame(t, br); !strings.Contains(pre, "retry: 1000") {
		t.Errorf("first frame = %q, want retry preamble", pre)
	}

	for _, payload := range []string{`{"n":1}`, `{"n":2}`} {
		s.send(t, payload)
		frame := readFrame(t, br)
		want := "data: " + payload + "\n"
		if frame != want {
			t.Errorf("frame = %q, want %q", frame, want)
		}
	}
}

func TestUpgradeGzipRoundTrip(t *testing.T)   { roundTrip(t, "gzip", "gzip") }
func TestUpgradeBrotliRoundTrip(t *testing.T) { roundTrip(t, "br", "br") }
func TestUpgradeZstdRoundTrip(t *testing.T)   { roundTrip(t, "zstd", "zstd") }

// A full browser-style header should land on brotli (server priority).
func TestUpgradeBrowserHeaderPrefersBrotli(t *testing.T) {
	roundTrip(t, "gzip, deflate, br, zstd", "br")
}

// A junk Accept-Encoding must fall back to identity: no Content-Encoding
// header and plain, readable frames.
func TestUpgradeJunkAcceptEncodingFallsBackToIdentity(t *testing.T) {
	s := startServer(t, nil)
	resp, br := s.open(t, "!!!garbage;;;, unsupported-thing")

	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty (identity)", got)
	}
	readFrame(t, br) // retry preamble
	s.send(t, `{"ok":true}`)
	if frame := readFrame(t, br); frame != `data: {"ok":true}`+"\n" {
		t.Errorf("frame = %q", frame)
	}
}

// Disabling compression must send identity even when the client offers
// brotli.
func TestUpgradeCompressionDisabled(t *testing.T) {
	s := startServer(t, nil, Options{Compression: Compression{Disabled: true}})
	resp, br := s.open(t, "br, gzip")

	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty when disabled", got)
	}
	readFrame(t, br)
	s.send(t, `{"plain":1}`)
	if frame := readFrame(t, br); frame != `data: {"plain":1}`+"\n" {
		t.Errorf("frame = %q", frame)
	}
}

// TestFlushDeliversEventPromptlyThroughCompressor proves each event is
// flushed through the compressor immediately: it reads incrementally and
// must receive an event without the server closing the stream. If the
// compressor buffered the event, the read would block until EOF and the
// timeout would fire.
func TestFlushDeliversEventPromptlyThroughCompressor(t *testing.T) {
	s := startServer(t, nil)
	_, br := s.open(t, "br")

	readFrame(t, br) // retry preamble

	s.send(t, `{"live":1}`)

	got := make(chan string, 1)
	go func() {
		var b strings.Builder
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\n" {
				got <- b.String()
				return
			}
			b.WriteString(line)
		}
	}()

	select {
	case frame := <-got:
		if frame != `data: {"live":1}`+"\n" {
			t.Errorf("frame = %q", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event not delivered promptly - compressor flush-per-event is broken")
	}
}

// flushHidingWriter forwards writes to the wrapped ResponseWriter but
// deliberately does not expose Flush. A direct http.Flusher assertion
// fails on it; http.NewResponseController must unwrap to reach the real
// flusher. This is the middleware shape the ResponseController fix
// exists for.
type flushHidingWriter struct{ http.ResponseWriter }

func (w flushHidingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestResponseControllerFlushThroughMiddleware(t *testing.T) {
	wrap := func(w http.ResponseWriter) http.ResponseWriter {
		return flushHidingWriter{w}
	}
	// Sanity: the wrapper really does hide Flush from a direct assertion.
	var probe http.ResponseWriter = flushHidingWriter{httptest.NewRecorder()}
	if _, ok := probe.(http.Flusher); ok {
		t.Fatal("test wrapper unexpectedly exposes http.Flusher")
	}

	s := startServer(t, wrap, Options{Compression: Compression{Disabled: true}})
	_, br := s.open(t, "")

	// If ResponseController could not unwrap to the real flusher, Upgrade
	// would have failed or events would never reach us. Reading the
	// preamble and an event proves the flush path works through the
	// wrapper.
	if pre := readFrame(t, br); !strings.Contains(pre, "retry: 1000") {
		t.Errorf("preamble = %q", pre)
	}
	s.send(t, `{"viamw":1}`)
	if frame := readFrame(t, br); frame != `data: {"viamw":1}`+"\n" {
		t.Errorf("frame = %q", frame)
	}
}

// TestSupportsFlush covers the capability check directly.
func TestSupportsFlush(t *testing.T) {
	if !supportsFlush(httptest.NewRecorder()) {
		t.Error("recorder should support flush")
	}
	if supportsFlush(&struct{ http.ResponseWriter }{httptest.NewRecorder()}) {
		t.Error("bare wrapper without Unwrap should not support flush")
	}
	if !supportsFlush(flushHidingWriter{httptest.NewRecorder()}) {
		t.Error("wrapper with Unwrap should support flush via the chain")
	}
}
