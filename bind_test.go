package poly_test

import (
	"strings"
	"testing"

	poly "github.com/jpl-au/fluent-poly"
	"github.com/jpl-au/fluent/html5/a"
	"github.com/jpl-au/fluent/html5/button"
	"github.com/jpl-au/fluent/html5/form"
	"github.com/jpl-au/fluent/html5/input"
)

func TestClickRendersDataAttribute(t *testing.T) {
	el := poly.Click(button.Text("+"), "increment")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-click="increment"`) {
		t.Errorf("expected data-poly-click attribute in HTML:\n%s", html)
	}
}

func TestSubmitRendersDataAttribute(t *testing.T) {
	el := poly.Submit(form.New(), "save")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-submit="save"`) {
		t.Errorf("expected data-poly-submit attribute in HTML:\n%s", html)
	}
}

func TestInputRendersDataAttribute(t *testing.T) {
	el := poly.Input(input.Text("name", ""), "update")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-input="update"`) {
		t.Errorf("expected data-poly-input attribute in HTML:\n%s", html)
	}
}

func TestLinkRendersDataAttribute(t *testing.T) {
	el := poly.Link(a.Link("/profile", "Profile"))
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-link=""`) {
		t.Errorf("expected data-poly-link attribute in HTML:\n%s", html)
	}
	if !strings.Contains(html, `href="/profile"`) {
		t.Errorf("expected href attribute in HTML:\n%s", html)
	}
}

func TestToggleClassRendersDataAttribute(t *testing.T) {
	el := poly.ToggleClass(button.Text("Menu"), "is-open")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-toggle-class="is-open"`) {
		t.Errorf("expected data-poly-toggle-class attribute in HTML:\n%s", html)
	}
}

func TestToggleTargetRendersDataAttribute(t *testing.T) {
	el := poly.ToggleTarget(button.Text("Menu"), "#nav")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-toggle-target="#nav"`) {
		t.Errorf("expected data-poly-toggle-target attribute in HTML:\n%s", html)
	}
}

func TestToggleAttrRendersDataAttribute(t *testing.T) {
	el := poly.ToggleAttr(button.Text("Toggle"), "hidden")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-toggle-attr="hidden"`) {
		t.Errorf("expected data-poly-toggle-attr attribute in HTML:\n%s", html)
	}
}

func TestToggleClassWithTargetChains(t *testing.T) {
	el := poly.ToggleClass(
		poly.ToggleTarget(button.Text("Menu"), "#nav"),
		"is-open",
	).Style("cursor: pointer")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-toggle-class="is-open"`) {
		t.Errorf("missing data-poly-toggle-class in HTML:\n%s", html)
	}
	if !strings.Contains(html, `data-poly-toggle-target="#nav"`) {
		t.Errorf("missing data-poly-toggle-target in HTML:\n%s", html)
	}
	if !strings.Contains(html, `style="cursor: pointer"`) {
		t.Errorf("missing style in HTML:\n%s", html)
	}
}

func TestTransitionRendersDataAttribute(t *testing.T) {
	el := poly.Transition(button.Text("Item"), "fade")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-transition="fade"`) {
		t.Errorf("expected data-poly-transition attribute in HTML:\n%s", html)
	}
}

func TestPreserveRendersDataAttribute(t *testing.T) {
	el := poly.Preserve(form.New())
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-preserve=""`) {
		t.Errorf("expected data-poly-preserve attribute in HTML:\n%s", html)
	}
}

func TestClickChains(t *testing.T) {
	// Verify the return type preserves chainability
	el := poly.Click(button.Text("+"), "increment").
		Style("cursor: pointer").
		Class("btn")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-click="increment"`) {
		t.Errorf("missing data-poly-click in HTML:\n%s", html)
	}
	if !strings.Contains(html, `style="cursor: pointer"`) {
		t.Errorf("missing style in HTML:\n%s", html)
	}
	if !strings.Contains(html, `class="btn"`) {
		t.Errorf("missing class in HTML:\n%s", html)
	}
}
