package poly

import (
	"log/slog"

	"github.com/jpl-au/fluent/node"
)

// Catch calls render and returns its result. If render panics, the
// panic is recovered, logged, and the fallback node is returned
// instead. This allows individual components to fail gracefully
// without taking down the entire page.
//
//	func render(s State) node.Node {
//	    return div.New(
//	        header(s),
//	        poly.Catch(func() node.Node {
//	            return riskyWidget(s)
//	        }, span.Text("Widget unavailable")),
//	        footer(s),
//	    )
//	}
func Catch(render func() node.Node, fallback node.Node) (result node.Node) {
	defer func() {
		if v := recover(); v != nil {
			slog.Error("poly.Catch recovered panic", "panic", v)
			result = fallback
		}
	}()
	return render()
}
