package sse

import (
	"bytes"
	"fmt"
	"testing"
)

// sampleEvent is a representative pre-encoded update: a JSON patch of
// the kind the session hands to Send.
var sampleEvent = []byte(`{"type":"update","patches":[{"key":"count","html":"<span>42</span>"}]}`)

// BenchmarkFrameEventAppend measures the previous framing approach: a
// fresh allocation per event via fmt.Appendf. Kept as the before-case
// baseline for the pooled version below.
func BenchmarkFrameEventAppend(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		frame := fmt.Appendf(nil, "data: %s\n\n", sampleEvent)
		_ = frame
	}
}

// BenchmarkFrameEventPooled measures the current framing approach: build
// the event in a buffer drawn from eventBufPool and return it. This is
// the work Send performs (the writer goroutine returns the buffer to the
// pool after writing), so the pool amortises the allocation away.
func BenchmarkFrameEventPooled(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := eventBufPool.Get().(*bytes.Buffer)
		buf.Reset()
		buf.WriteString("data: ")
		buf.Write(sampleEvent)
		buf.WriteString("\n\n")
		eventBufPool.Put(buf)
	}
}
