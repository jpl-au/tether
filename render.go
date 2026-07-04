package tether

import (
	"bytes"
	"crypto/rand"
	"html"
	"io"
	"strconv"
	"time"

	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/wire"
)

// extension maps a data attribute marker to the JS file that handles it.
// When the rendered HTML contains the marker, the script is included.
type extension struct {
	marker []byte // e.g. []byte("data-tether-upload")
	script string // e.g. "tether-upload.js"
}

// extensions is the registry of optional JS files. Add entries here
// when new extension scripts are created in client/. Keep the list in
// sync with extensionMarkers in client/tether.js, which handles the
// lazy-load case where a marker first appears after a morph.
var extensions = []extension{
	{marker: []byte("data-tether-upload"), script: "tether-upload.js"},
	{marker: []byte("data-tether-draggable"), script: "tether-drag-and-drop.js"},
	{marker: []byte("data-tether-sortable"), script: "tether-drag-and-drop.js"},
	{marker: []byte("data-tether-swipe"), script: "tether-touch.js"},
	{marker: []byte("data-tether-longpress"), script: "tether-touch.js"},
	{marker: []byte("data-tether-hotkey"), script: "tether-hotkey.js"},
	{marker: []byte("data-tether-timer"), script: "tether-timer.js"},
	{marker: []byte("data-tether-selectable"), script: "tether-select.js"},
	{marker: []byte("data-tether-template"), script: "tether-template.js"},
}

// tetherBody implements node.Node for the tether root div and client
// scripts. It exists so the Layout function in StatefulConfig can receive a
// composable node and wrap it in a full HTML document (head, body, etc.)
// rather than dealing with raw bytes. When Layout is nil, tetherBody
// renders directly into the response as a bare fragment.
type tetherBody struct {
	html              []byte
	endpoint          string
	session           string
	transport         mode.Transport
	retryDelay        time.Duration
	maxRetryDelay     time.Duration
	backoffMultiplier float64
	jitter            bool
	defaultDebounce   time.Duration
	transitionTimeout time.Duration
	flashDuration     time.Duration
	toastDuration     time.Duration
	wireFormat        wire.Format
	worker            bool
	pushKey           string
	backgroundSync    bool
	syncRetention     time.Duration
	runtime           ClientRuntime
}

// Render writes the body to w. Write errors are intentionally not
// checked: this writes to an http.ResponseWriter during the initial
// GET, and a failure means the client disconnected - a normal
// condition that requires no action. Use WriteTo to observe it.
func (p *tetherBody) Render(w io.Writer) {
	_, _ = p.WriteTo(w)
}

// WriteTo writes the body to w, returning the byte count and any
// write error. Satisfies io.WriterTo.
func (p *tetherBody) WriteTo(w io.Writer) (int64, error) {
	var buf bytes.Buffer
	p.RenderBuilder(&buf)
	return buf.WriteTo(w)
}

// RenderBytes returns the body as a byte slice.
func (p *tetherBody) RenderBytes() []byte {
	var buf bytes.Buffer
	p.RenderBuilder(&buf)
	return buf.Bytes()
}

func (p *tetherBody) RenderBuilder(buf *bytes.Buffer) {
	// Inject default styles: cloak hiding, reconnect bar, and toast.
	// Cosmetic properties use the class so developers can override them
	// without fighting inline style specificity.
	buf.WriteString("<style>")
	buf.WriteString("[data-tether-cloak]{display:none!important}")
	buf.WriteString(".tether-reconnecting{background:#ef4444;color:#fff;text-align:center;padding:6px 12px;font:14px/1.4 system-ui,sans-serif}")
	buf.WriteString(".tether-toast{background:#333;color:#fff;padding:10px 20px;border-radius:8px;font:14px/1.4 system-ui,sans-serif;box-shadow:0 4px 12px rgba(0,0,0,.15)}")
	buf.WriteString("</style>")
	buf.WriteString(`<div data-tether-root data-tether-endpoint="`)
	buf.WriteString(html.EscapeString(p.endpoint))
	buf.WriteString(`"`)
	if p.session != "" {
		buf.WriteString(` data-tether-session="`)
		buf.WriteString(html.EscapeString(p.session))
		buf.WriteString(`"`)
	}
	switch p.transport {
	case mode.ServerSentEvents:
		buf.WriteString(` data-tether-transport="sse"`)
	case mode.Both:
		buf.WriteString(` data-tether-transport="auto"`)
	case mode.HTTP:
		buf.WriteString(` data-tether-transport="fetch"`)
	default:
		buf.WriteString(` data-tether-transport="ws"`)
	}
	// Pass JS runtime configuration as data attributes so the client
	// reads them instead of using hardcoded values. Retry-delay and
	// max-retry-delay are omitted for fetch mode - there is no
	// persistent connection to reconnect.
	if p.transport != mode.HTTP {
		buf.WriteString(` data-tether-retry-delay="`)
		buf.WriteString(strconv.FormatInt(p.retryDelay.Milliseconds(), 10))
		buf.WriteString(`" data-tether-max-retry-delay="`)
		buf.WriteString(strconv.FormatInt(p.maxRetryDelay.Milliseconds(), 10))
		buf.WriteString(`" data-tether-backoff-multiplier="`)
		buf.WriteString(strconv.FormatFloat(p.backoffMultiplier, 'f', -1, 64))
		buf.WriteString(`"`)
		if p.jitter {
			buf.WriteString(` data-tether-jitter`)
		}
	}
	buf.WriteString(` data-tether-debounce-default="`)
	buf.WriteString(strconv.FormatInt(p.defaultDebounce.Milliseconds(), 10))
	buf.WriteString(`" data-tether-transition-timeout="`)
	buf.WriteString(strconv.FormatInt(p.transitionTimeout.Milliseconds(), 10))
	buf.WriteString(`" data-tether-flash-duration="`)
	buf.WriteString(strconv.FormatInt(p.flashDuration.Milliseconds(), 10))
	buf.WriteString(`" data-tether-toast-duration="`)
	buf.WriteString(strconv.FormatInt(p.toastDuration.Milliseconds(), 10))
	buf.WriteString(`"`)
	if p.worker {
		buf.WriteString(` data-tether-worker`)
	}
	if p.pushKey != "" {
		buf.WriteString(` data-tether-push-key="`)
		buf.WriteString(html.EscapeString(p.pushKey))
		buf.WriteString(`"`)
	}
	if p.backgroundSync {
		buf.WriteString(` data-tether-background-sync`)
		buf.WriteString(` data-tether-sync-retention="`)
		buf.WriteString(strconv.FormatInt(p.syncRetention.Milliseconds(), 10))
		buf.WriteString(`"`)
	}
	if dev.Enabled() {
		buf.WriteString(` data-tether-dev`)
	}
	// Only emit wire format for non-default encodings. JSON is the
	// default, so absence means JSON.
	if p.wireFormat != wire.JSON {
		buf.WriteString(` data-tether-wire-format="`)
		buf.WriteString(p.wireFormat.String())
		buf.WriteString(`"`)
	}
	rt := p.runtime
	if rt == nil {
		rt = Runtime.Default()
	}
	rt.writeAttributes(buf)
	buf.WriteString(`>`)
	buf.Write(p.html)
	rt.writeScripts(buf, p.html)

	// Wire format extensions are injected after the runtime scripts.
	// The extension overrides the default JSON decoder in tether.js
	// with a format-specific decoder (e.g. CBOR). Only relevant for
	// the JS runtime; WASM handles decoding in Go.
	if _, isJS := rt.(jsRuntime); isJS && p.wireFormat == wire.CBOR {
		v := clientVersion()
		buf.WriteString("<script src=\"/_tether/tether-wire-cbor.js?v=")
		buf.WriteString(v)
		buf.WriteString("\"></script>\n")
	}
}

func (p *tetherBody) Nodes() []node.Node { return nil }

// newID generates a cryptographically random session identifier using
// Go's crypto/rand.Text (base-32, no padding). The session ID doubles
// as a bearer token - knowing it is sufficient to send events to the
// session - so it must be unguessable.
func newID() string {
	return rand.Text()
}
