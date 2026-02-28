package poly

import (
	"bytes"
	"crypto/rand"
	"html"
	"io"
	"strconv"
	"time"

	"github.com/jpl-au/fluent-poly/mode"
	"github.com/jpl-au/fluent/node"
)

// extension maps a data attribute marker to the JS file that handles it.
// When the rendered HTML contains the marker, the script is included.
type extension struct {
	marker []byte // e.g. []byte("data-poly-upload")
	script string // e.g. "fluent-poly-upload.js"
}

// extensions is the registry of optional JS files. Add entries here
// when new extension scripts are created in client/.
var extensions = []extension{
	{marker: []byte("data-poly-upload"), script: "fluent-poly-upload.js"},
}

// polyBody implements node.Node for the poly root div and client
// scripts. It exists so the Layout function in Config can receive a
// composable node and wrap it in a full HTML document (head, body, etc.)
// rather than dealing with raw bytes. When Layout is nil, polyBody
// renders directly into the response as a bare fragment.
type polyBody struct {
	html              []byte
	endpoint          string
	session           string
	transport         mode.Transport
	retryDelay        time.Duration
	maxRetryDelay     time.Duration
	defaultDebounce   time.Duration
	transitionTimeout time.Duration
	worker            bool
	pushKey           string
	backgroundSync    bool
	devMode           bool
}

func (p *polyBody) Render(w ...io.Writer) []byte {
	var buf bytes.Buffer
	p.RenderBuilder(&buf)
	if len(w) > 0 && w[0] != nil {
		buf.WriteTo(w[0])
		return nil
	}
	return buf.Bytes()
}

func (p *polyBody) RenderBuilder(buf *bytes.Buffer) {
	// Inject a style rule that hides cloaked elements before JS loads.
	// The JS runtime removes the attribute on init, revealing them.
	buf.WriteString("<style>[data-poly-cloak]{display:none!important}</style>")
	buf.WriteString(`<div data-poly-root data-poly-endpoint="`)
	buf.WriteString(html.EscapeString(p.endpoint))
	buf.WriteString(`"`)
	if p.session != "" {
		buf.WriteString(` data-poly-session="`)
		buf.WriteString(html.EscapeString(p.session))
		buf.WriteString(`"`)
	}
	switch p.transport {
	case mode.SSE:
		buf.WriteString(` data-poly-transport="sse"`)
	case mode.Auto:
		buf.WriteString(` data-poly-transport="auto"`)
	case mode.Fetch:
		buf.WriteString(` data-poly-transport="fetch"`)
	default:
		buf.WriteString(` data-poly-transport="ws"`)
	}
	// Pass JS runtime configuration as data attributes so the client
	// reads them instead of using hardcoded values. Retry-delay and
	// max-retry-delay are omitted for fetch mode — there is no
	// persistent connection to reconnect.
	if p.transport != mode.Fetch {
		buf.WriteString(` data-poly-retry-delay="`)
		buf.WriteString(strconv.FormatInt(p.retryDelay.Milliseconds(), 10))
		buf.WriteString(`" data-poly-max-retry-delay="`)
		buf.WriteString(strconv.FormatInt(p.maxRetryDelay.Milliseconds(), 10))
		buf.WriteString(`"`)
	}
	buf.WriteString(` data-poly-debounce-default="`)
	buf.WriteString(strconv.FormatInt(p.defaultDebounce.Milliseconds(), 10))
	buf.WriteString(`" data-poly-transition-timeout="`)
	buf.WriteString(strconv.FormatInt(p.transitionTimeout.Milliseconds(), 10))
	buf.WriteString(`"`)
	if p.worker {
		buf.WriteString(` data-poly-worker`)
	}
	if p.pushKey != "" {
		buf.WriteString(` data-poly-push-key="`)
		buf.WriteString(html.EscapeString(p.pushKey))
		buf.WriteString(`"`)
	}
	if p.backgroundSync {
		buf.WriteString(` data-poly-background-sync`)
	}
	if p.devMode {
		buf.WriteString(` data-poly-dev`)
	}
	buf.WriteString(`>`)
	buf.Write(p.html)

	// Append a content hash to all script URLs so browsers fetch fresh
	// copies after a library upgrade, even without a service worker.
	v := clientVersion()
	buf.WriteString("</div>\n<script src=\"/_poly/idiomorph.min.js?v=")
	buf.WriteString(v)
	buf.WriteString("\"></script>\n<script src=\"/_poly/fluent-poly.js?v=")
	buf.WriteString(v)
	buf.WriteString("\"></script>\n")

	// Extension scripts are included only when the rendered HTML uses
	// the corresponding data attributes. This keeps the client payload
	// small for apps that don't need optional features.
	for _, ext := range extensions {
		if bytes.Contains(p.html, ext.marker) {
			buf.WriteString("<script src=\"/_poly/")
			buf.WriteString(ext.script)
			buf.WriteString("?v=")
			buf.WriteString(v)
			buf.WriteString("\"></script>\n")
		}
	}
}

func (p *polyBody) Nodes() []node.Node { return nil }

// newID generates a cryptographically random session identifier using
// Go's crypto/rand.Text (base-32, no padding). The session ID doubles
// as a bearer token — knowing it is sufficient to send events to the
// session — so it must be unguessable.
func newID() string {
	return rand.Text()
}
