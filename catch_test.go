package tether

import (
	"testing"

	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func TestCatchReturnsNormalResult(t *testing.T) {
	result := Catch(func() node.Node {
		return div.Text("hello")
	}, span.Text("fallback"))

	html := string(result.Render())
	if html != "<div>hello</div>" {
		t.Errorf("expected <div>hello</div>, got %s", html)
	}
}

func TestCatchReturnsFallbackOnPanic(t *testing.T) {
	result := Catch(func() node.Node {
		panic("render failed")
	}, span.Text("fallback"))

	html := string(result.Render())
	if html != "<span>fallback</span>" {
		t.Errorf("expected <span>fallback</span>, got %s", html)
	}
}

func TestCatchReturnsFallbackOnNilPanic(t *testing.T) {
	result := Catch(func() node.Node {
		var s *string
		_ = *s // nil pointer dereference
		return div.Text("unreachable")
	}, span.Text("recovered"))

	html := string(result.Render())
	if html != "<span>recovered</span>" {
		t.Errorf("expected <span>recovered</span>, got %s", html)
	}
}
