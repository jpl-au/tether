package tether

import (
	"bytes"
	"html"
)

// ClientRuntime controls which browser-side scripts the framework
// injects after the tether root element. The default JS runtime
// injects idiomorph and tether.js. A WASM runtime injects the Go
// WASM loader and support scripts instead.
type ClientRuntime interface {
	// writeAttributes emits additional data attributes for the tether
	// root element. Called during root div construction, before the
	// closing >.
	writeAttributes(buf *bytes.Buffer)

	// writeScripts emits script tags after the tether root element.
	// The html parameter contains the rendered page content, used by
	// the JS runtime to detect extension markers.
	writeScripts(buf *bytes.Buffer, html []byte)
}

// Runtime provides constructors for the built-in client runtimes.
// Use Runtime.Default() for the standard JS client (idiomorph and
// tether.js) or Runtime.WASM() for a Go WASM client.
var Runtime runtimeFactory

type runtimeFactory struct{}

// Default returns the standard JS client runtime. It injects
// idiomorph.min.js, tether.js, and any extension scripts whose
// data attribute markers appear in the rendered HTML.
func (runtimeFactory) Default() ClientRuntime {
	return jsRuntime{}
}

// WASM returns a runtime for a Go WASM client. The src argument
// is the URL path to the compiled .wasm blob. The framework
// injects the tether-wasm.js bootstrap script automatically.
//
//	tether.Runtime.WASM("/static/client.go.wasm")
func (runtimeFactory) WASM(src string) ClientRuntime {
	return wasmRuntime{src: src}
}

// jsRuntime injects the default tether JS client.
type jsRuntime struct{}

func (jsRuntime) writeAttributes(_ *bytes.Buffer) {}

func (jsRuntime) writeScripts(buf *bytes.Buffer, content []byte) {
	// Append a content hash to all script URLs so browsers fetch fresh
	// copies after a library upgrade, even without a service worker.
	v := clientVersion()
	buf.WriteString("</div>\n<script src=\"/_tether/idiomorph.min.js?v=")
	buf.WriteString(v)
	buf.WriteString("\"></script>\n<script src=\"/_tether/tether.js?v=")
	buf.WriteString(v)
	buf.WriteString("\"></script>\n")

	// Extension scripts are included only when the rendered HTML uses
	// the corresponding data attributes. This keeps the client payload
	// small for apps that don't need optional features.
	for _, ext := range extensions {
		if bytes.Contains(content, ext.marker) {
			buf.WriteString("<script src=\"/_tether/")
			buf.WriteString(ext.script)
			buf.WriteString("?v=")
			buf.WriteString(v)
			buf.WriteString("\"></script>\n")
		}
	}
}

// wasmRuntime injects the tether WASM bootstrap script and passes
// the .wasm blob path via a data attribute on the root element.
type wasmRuntime struct {
	src string // path to the .wasm blob
}

func (w wasmRuntime) writeAttributes(buf *bytes.Buffer) {
	buf.WriteString(` data-tether-wasm-src="`)
	buf.WriteString(html.EscapeString(w.src))
	buf.WriteString(`"`)
}

func (w wasmRuntime) writeScripts(buf *bytes.Buffer, _ []byte) {
	v := clientVersion()
	buf.WriteString("</div>\n<script src=\"/_tether/tether-wasm.js?v=")
	buf.WriteString(v)
	buf.WriteString("\"></script>\n")
}
