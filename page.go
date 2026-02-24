package poly

import (
	"bytes"
	"crypto/rand"
	"io"

	"github.com/jpl-au/fluent/node"
)

// polyBody is a node.Node that renders the poly root div (with the
// pre-rendered session content inside) and the client script tags.
// It exists so the Layout function receives a composable node rather
// than raw bytes.
type polyBody struct {
	html      []byte
	endpoint  string
	session   string
	transport TransportMode
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
	buf.WriteString(p.endpoint)
	buf.WriteString(`" data-poly-session="`)
	buf.WriteString(p.session)
	buf.WriteString(`"`)
	switch p.transport {
	case SSEOnly:
		buf.WriteString(` data-poly-transport="sse"`)
	case WebSocketWithFallback:
		buf.WriteString(` data-poly-transport="auto"`)
	default:
		buf.WriteString(` data-poly-transport="ws"`)
	}
	buf.WriteString(`>`)
	buf.Write(p.html)
	buf.WriteString("</div>\n<script src=\"/_poly/idiomorph.min.js\"></script>\n<script src=\"/_poly/fluent-poly.js\"></script>\n")
}

func (p *polyBody) Nodes() []node.Node { return nil }

// newID generates a cryptographically random session identifier
// using base-32 encoding (alphanumeric, no padding).
func newID() string {
	return rand.Text()
}
