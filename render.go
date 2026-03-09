package tether

import (
	"bytes"
	"crypto/rand"
	"html"
	"io"
	"strconv"
	"time"

	"github.com/jpl-au/fluent-tether/dev"
	"github.com/jpl-au/fluent-tether/mode"
	"github.com/jpl-au/fluent/node"
)

// extension maps a data attribute marker to the JS file that handles it.
// When the rendered HTML contains the marker, the script is included.
type extension struct {
	marker []byte // e.g. []byte("data-tether-upload")
	script string // e.g. "fluent-tether-upload.js"
}

// extensions is the registry of optional JS files. Add entries here
// when new extension scripts are created in client/.
var extensions = []extension{
	{marker: []byte("data-tether-upload"), script: "fluent-tether-upload.js"},
}

// tetherBody implements node.Node for the tether root div and client
// scripts. It exists so the Layout function in Config can receive a
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
	defaultDebounce   time.Duration
	transitionTimeout time.Duration
	flashDuration     time.Duration
	toastDuration     time.Duration
	worker            bool
	pushKey           string
	backgroundSync    bool
	syncRetention     time.Duration
}

func (p *tetherBody) Render(w ...io.Writer) []byte {
	var buf bytes.Buffer
	p.RenderBuilder(&buf)
	if len(w) > 0 && w[0] != nil {
		buf.WriteTo(w[0])
		return nil
	}
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
	// max-retry-delay are omitted for fetch mode — there is no
	// persistent connection to reconnect.
	if p.transport != mode.HTTP {
		buf.WriteString(` data-tether-retry-delay="`)
		buf.WriteString(strconv.FormatInt(p.retryDelay.Milliseconds(), 10))
		buf.WriteString(`" data-tether-max-retry-delay="`)
		buf.WriteString(strconv.FormatInt(p.maxRetryDelay.Milliseconds(), 10))
		buf.WriteString(`"`)
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
	buf.WriteString(`>`)
	buf.Write(p.html)

	// Append a content hash to all script URLs so browsers fetch fresh
	// copies after a library upgrade, even without a service worker.
	v := clientVersion()
	buf.WriteString("</div>\n<script src=\"/_tether/idiomorph.min.js?v=")
	buf.WriteString(v)
	buf.WriteString("\"></script>\n<script src=\"/_tether/fluent-tether.js?v=")
	buf.WriteString(v)
	buf.WriteString("\"></script>\n")

	// Extension scripts are included only when the rendered HTML uses
	// the corresponding data attributes. This keeps the client payload
	// small for apps that don't need optional features.
	for _, ext := range extensions {
		if bytes.Contains(p.html, ext.marker) {
			buf.WriteString("<script src=\"/_tether/")
			buf.WriteString(ext.script)
			buf.WriteString("?v=")
			buf.WriteString(v)
			buf.WriteString("\"></script>\n")
		}
	}
}

func (p *tetherBody) Nodes() []node.Node { return nil }

// newID generates a cryptographically random session identifier using
// Go's crypto/rand.Text (base-32, no padding). The session ID doubles
// as a bearer token — knowing it is sufficient to send events to the
// session — so it must be unguessable.
func newID() string {
	return rand.Text()
}
