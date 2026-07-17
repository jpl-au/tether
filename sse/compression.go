package sse

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// Compression configures response compression for the SSE stream.
// Compression is enabled by default with sensible defaults - set
// Disabled to opt out. When enabled, the transport negotiates an
// algorithm against the client's Accept-Encoding header and sets the
// Content-Encoding response header. The browser (EventSource or fetch)
// decompresses transparently, so no client change is needed.
//
// SSE payloads are HTML fragments and JSON patches - highly
// compressible text - so compression typically cuts stream bandwidth by
// well over half. Each event is flushed through the compressor
// immediately, so compression does not add latency.
type Compression struct {
	// Disabled turns off response compression. Use this when a reverse
	// proxy already compresses the stream, or for debugging.
	Disabled bool

	// Level sets the compression effort. Zero defaults to
	// [CompressionFastest], the best trade-off for real-time updates
	// where latency matters more than a few extra bytes on the wire.
	Level CompressionLevel
}

// CompressionLevel selects how hard the compressor works. The concrete
// per-algorithm level is derived from this - brotli quality, zstd
// speed, and gzip/deflate level all map onto the same three tiers so
// the choice reads the same regardless of which algorithm the client
// negotiates.
type CompressionLevel int

const (
	// CompressionFastest uses the least CPU per event. This is the
	// default and the best choice for real-time updates where latency
	// matters more than payload size.
	CompressionFastest CompressionLevel = 1

	// CompressionBalanced trades some CPU for a better ratio. Useful
	// when bandwidth is more constrained than CPU.
	CompressionBalanced CompressionLevel = 6

	// CompressionSmallest produces the smallest stream at the highest
	// CPU cost. Rarely appropriate for real-time traffic.
	CompressionSmallest CompressionLevel = 9
)

// compressor is a compressing writer wrapped around the HTTP response.
// Compressors buffer internally, so after every SSE event the transport
// must Flush to drain buffered bytes into the response writer - without
// that flush the event sits in the compressor and the client sees
// nothing. Close is called once when the stream ends.
type compressor interface {
	io.Writer
	Flush() error
	Close() error
}

// buildFunc constructs a compressor around an underlying writer.
type buildFunc func(io.Writer, CompressionLevel) (compressor, error)

// candidate pairs a Content-Encoding token with its constructor.
type candidate struct {
	encoding string
	build    buildFunc
}

// candidates lists the supported algorithms in server-preference order:
// strongest ratio for text first. Negotiation walks this list and picks
// the first entry the client accepts, so the server drives the choice
// while still honouring the client's capabilities.
var candidates = []candidate{
	{"br", newBrotli},
	{"zstd", newZstd},
	{"gzip", newGzip},
	{"deflate", newDeflate},
}

// negotiate picks a compressor for the client's Accept-Encoding header.
// It returns the Content-Encoding token to advertise and a constructor
// bound to the requested level. When the client accepts none of the
// algorithms we support (or the header is empty or junk) it returns
// "", nil and the stream is sent uncompressed (identity encoding).
func negotiate(acceptEncoding string, level CompressionLevel) (string, func(io.Writer) (compressor, error)) {
	accepted := acceptedEncodings(acceptEncoding)
	for _, c := range candidates {
		if accepted[c.encoding] {
			build := c.build
			return c.encoding, func(w io.Writer) (compressor, error) {
				return build(w, level)
			}
		}
	}
	return "", nil
}

// acceptedEncodings parses an Accept-Encoding header into the set of
// encodings the client is willing to receive. It is deliberately
// lenient: malformed entries are skipped rather than rejecting the
// whole header, so a junk value simply yields an empty (identity) set
// instead of an error. An explicit q=0 excludes an encoding per
// RFC 9110 - "gzip;q=0" means the client refuses gzip.
func acceptedEncodings(header string) map[string]bool {
	accepted := make(map[string]bool)
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		if refused(params) {
			continue
		}
		accepted[token] = true
	}
	return accepted
}

// refused reports whether the encoding parameters carry a q-value of
// zero. A malformed q-value is treated as no preference (not refused),
// keeping negotiation resilient to junk input.
func refused(params string) bool {
	for _, p := range strings.Split(params, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || strings.ToLower(strings.TrimSpace(key)) != "q" {
			continue
		}
		if q, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && q == 0 {
			return true
		}
	}
	return false
}

// newBrotli builds a brotli compressor. Brotli gives the best ratio on
// HTML and JSON, which is why it leads the preference order.
func newBrotli(w io.Writer, level CompressionLevel) (compressor, error) {
	quality := brotli.BestSpeed
	switch level {
	case CompressionBalanced:
		quality = brotli.DefaultCompression
	case CompressionSmallest:
		quality = brotli.BestCompression
	}
	return brotli.NewWriterLevel(w, quality), nil
}

// newZstd builds a zstd compressor pinned to a single encoder goroutine
// - the default fans out across GOMAXPROCS workers, which is wasteful
// for the small, frequent writes of an SSE stream.
func newZstd(w io.Writer, level CompressionLevel) (compressor, error) {
	speed := zstd.SpeedFastest
	switch level {
	case CompressionBalanced:
		speed = zstd.SpeedDefault
	case CompressionSmallest:
		speed = zstd.SpeedBestCompression
	}
	return zstd.NewWriter(w,
		zstd.WithEncoderLevel(speed),
		zstd.WithEncoderConcurrency(1),
	)
}

// newGzip builds a gzip compressor. gzip is the universal fallback -
// every browser accepts it.
func newGzip(w io.Writer, level CompressionLevel) (compressor, error) {
	return gzip.NewWriterLevel(w, int(level))
}

// newDeflate builds a raw DEFLATE compressor for the rare client that
// advertises deflate but not gzip.
func newDeflate(w io.Writer, level CompressionLevel) (compressor, error) {
	return flate.NewWriter(w, int(level))
}

// supportsFlush reports whether w, or a writer it unwraps to, can flush
// buffered bytes to the client. SSE is unusable without flushing, so
// the transport checks this up front and fails with a clear error
// rather than writing headers and discovering the problem mid-stream.
// This mirrors the unwrap walk that [http.ResponseController] performs
// internally, letting us detect the capability before committing the
// response.
func supportsFlush(w http.ResponseWriter) bool {
	for {
		switch w.(type) {
		case http.Flusher:
			return true
		case interface{ FlushError() error }:
			return true
		}
		unwrapper, ok := w.(interface {
			Unwrap() http.ResponseWriter
		})
		if !ok {
			return false
		}
		w = unwrapper.Unwrap()
	}
}
