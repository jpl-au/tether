package poly

import (
	"bytes"
	"crypto/rand"
	"html"
	"io"
	"strconv"
	"time"

	"github.com/jpl-au/fluent/node"
)

// polyBody implements node.Node for the poly root div and client
// scripts. It exists so the Layout function in Config can receive a
// composable node and wrap it in a full HTML document (head, body, etc.)
// rather than dealing with raw bytes. When Layout is nil, polyBody
// renders directly into the response as a bare fragment.
type polyBody struct {
	html              []byte
	endpoint          string
	session           string
	transport         TransportMode
	retryDelay        time.Duration
	maxRetryDelay     time.Duration
	defaultDebounce   time.Duration
	transitionTimeout time.Duration
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
	buf.WriteString(`<div data-poly-root data-poly-endpoint="`)
	buf.WriteString(html.EscapeString(p.endpoint))
	buf.WriteString(`" data-poly-session="`)
	buf.WriteString(html.EscapeString(p.session))
	buf.WriteString(`"`)
	switch p.transport {
	case SSEOnly:
		buf.WriteString(` data-poly-transport="sse"`)
	case WebSocketWithFallback:
		buf.WriteString(` data-poly-transport="auto"`)
	default:
		buf.WriteString(` data-poly-transport="ws"`)
	}
	// Pass JS runtime configuration as data attributes so the client
	// reads them instead of using hardcoded values.
	buf.WriteString(` data-poly-retry-delay="`)
	buf.WriteString(strconv.FormatInt(p.retryDelay.Milliseconds(), 10))
	buf.WriteString(`" data-poly-max-retry-delay="`)
	buf.WriteString(strconv.FormatInt(p.maxRetryDelay.Milliseconds(), 10))
	buf.WriteString(`" data-poly-debounce-default="`)
	buf.WriteString(strconv.FormatInt(p.defaultDebounce.Milliseconds(), 10))
	buf.WriteString(`" data-poly-transition-timeout="`)
	buf.WriteString(strconv.FormatInt(p.transitionTimeout.Milliseconds(), 10))
	buf.WriteString(`">`)
	buf.Write(p.html)
	buf.WriteString("</div>\n<script src=\"/_poly/idiomorph.min.js\"></script>\n<script src=\"/_poly/fluent-poly.js\"></script>\n")
}

func (p *polyBody) Nodes() []node.Node { return nil }

// newID generates a cryptographically random session identifier using
// Go's crypto/rand.Text (base-32, no padding). The session ID doubles
// as a bearer token — knowing it is sufficient to send events to the
// session — so it must be unguessable.
func newID() string {
	return rand.Text()
}
