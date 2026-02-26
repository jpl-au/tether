package poly_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jpl-au/fluent-poly/bind"
	"github.com/jpl-au/fluent/html5/a"
	"github.com/jpl-au/fluent/html5/button"
	"github.com/jpl-au/fluent/html5/dropdown"
	"github.com/jpl-au/fluent/html5/form"
	"github.com/jpl-au/fluent/html5/input"
)

func TestClickRendersDataAttribute(t *testing.T) {
	el := bind.Click(button.Text("+"), "increment")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-click="increment"`) {
		t.Errorf("expected data-poly-click attribute in HTML:\n%s", html)
	}
}

func TestSubmitRendersDataAttribute(t *testing.T) {
	el := bind.Submit(form.New(), "save")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-submit="save"`) {
		t.Errorf("expected data-poly-submit attribute in HTML:\n%s", html)
	}
}

func TestInputRendersDataAttribute(t *testing.T) {
	el := bind.Input(input.Text("name", ""), "update")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-input="update"`) {
		t.Errorf("expected data-poly-input attribute in HTML:\n%s", html)
	}
}

func TestLinkRendersDataAttribute(t *testing.T) {
	el := bind.Link(a.Link("/profile", "Profile"))
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-link=""`) {
		t.Errorf("expected data-poly-link attribute in HTML:\n%s", html)
	}
	if !strings.Contains(html, `href="/profile"`) {
		t.Errorf("expected href attribute in HTML:\n%s", html)
	}
}

func TestToggleClassRendersDataAttribute(t *testing.T) {
	el := bind.ToggleClass(button.Text("Menu"), "is-open")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-toggle-class="is-open"`) {
		t.Errorf("expected data-poly-toggle-class attribute in HTML:\n%s", html)
	}
}

func TestToggleTargetRendersDataAttribute(t *testing.T) {
	el := bind.ToggleTarget(button.Text("Menu"), "#nav")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-toggle-target="#nav"`) {
		t.Errorf("expected data-poly-toggle-target attribute in HTML:\n%s", html)
	}
}

func TestToggleAttrRendersDataAttribute(t *testing.T) {
	el := bind.ToggleAttr(button.Text("Toggle"), "hidden")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-toggle-attr="hidden"`) {
		t.Errorf("expected data-poly-toggle-attr attribute in HTML:\n%s", html)
	}
}

func TestToggleClassWithTargetChains(t *testing.T) {
	el := bind.ToggleClass(
		bind.ToggleTarget(button.Text("Menu"), "#nav"),
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
	el := bind.Transition(button.Text("Item"), "fade")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-transition="fade"`) {
		t.Errorf("expected data-poly-transition attribute in HTML:\n%s", html)
	}
}

func TestPreserveRendersDataAttribute(t *testing.T) {
	el := bind.Preserve(form.New())
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-preserve=""`) {
		t.Errorf("expected data-poly-preserve attribute in HTML:\n%s", html)
	}
}

func TestAutoFocusRendersDataAttribute(t *testing.T) {
	el := bind.AutoFocus(input.Text("name", ""))
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-autofocus=""`) {
		t.Errorf("expected data-poly-autofocus attribute in HTML:\n%s", html)
	}
}

func TestHookRendersDataAttribute(t *testing.T) {
	el := bind.Hook(button.Text("Chart"), "chart")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-hook="chart"`) {
		t.Errorf("expected data-poly-hook attribute in HTML:\n%s", html)
	}
}

func TestDisableRendersDataAttribute(t *testing.T) {
	el := bind.Disable(bind.Click(button.Text("Save"), "save"), "Saving...")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-disable="Saving..."`) {
		t.Errorf("expected data-poly-disable attribute in HTML:\n%s", html)
	}
}

func TestDisableEmptyTextRendersDataAttribute(t *testing.T) {
	el := bind.Disable(bind.Click(button.Text("Go"), "go"), "")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-disable=""`) {
		t.Errorf("expected data-poly-disable attribute in HTML:\n%s", html)
	}
}

func TestConfirmRendersDataAttribute(t *testing.T) {
	el := bind.Confirm(bind.Click(button.Text("Delete"), "delete"), "Are you sure?")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-confirm="Are you sure?"`) {
		t.Errorf("expected data-poly-confirm attribute in HTML:\n%s", html)
	}
}

func TestDebounceRendersDataAttribute(t *testing.T) {
	el := bind.Debounce(bind.Input(input.Text("q", ""), "search"), 500*time.Millisecond)
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-debounce="500"`) {
		t.Errorf("expected data-poly-debounce attribute in HTML:\n%s", html)
	}
}

func TestThrottleRendersDataAttribute(t *testing.T) {
	el := bind.Throttle(bind.Click(button.Text("Go"), "fire"), 1*time.Second)
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-throttle="1000"`) {
		t.Errorf("expected data-poly-throttle attribute in HTML:\n%s", html)
	}
}

func TestChangeRendersDataAttribute(t *testing.T) {
	el := bind.Change(dropdown.New(), "filter")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-change="filter"`) {
		t.Errorf("expected data-poly-change attribute in HTML:\n%s", html)
	}
}

func TestKeyDownRendersDataAttribute(t *testing.T) {
	el := bind.KeyDown(input.Text("cmd", ""), "exec")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-keydown="exec"`) {
		t.Errorf("expected data-poly-keydown attribute in HTML:\n%s", html)
	}
}

func TestFocusRendersDataAttribute(t *testing.T) {
	el := bind.Focus(input.Text("name", ""), "focus-name")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-focus="focus-name"`) {
		t.Errorf("expected data-poly-focus attribute in HTML:\n%s", html)
	}
}

func TestBlurRendersDataAttribute(t *testing.T) {
	el := bind.Blur(input.Text("name", ""), "blur-name")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-blur="blur-name"`) {
		t.Errorf("expected data-poly-blur attribute in HTML:\n%s", html)
	}
}

func TestFilterKeyRendersDataAttribute(t *testing.T) {
	el := bind.FilterKey(bind.KeyDown(input.Text("cmd", ""), "exec"), "Enter")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-key="Enter"`) {
		t.Errorf("expected data-poly-key attribute in HTML:\n%s", html)
	}
	if !strings.Contains(html, `data-poly-keydown="exec"`) {
		t.Errorf("expected data-poly-keydown attribute in HTML:\n%s", html)
	}
}

func TestFocusTrapRendersDataAttribute(t *testing.T) {
	el := bind.FocusTrap(button.Text("Dialog"))
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-focus-trap=""`) {
		t.Errorf("expected data-poly-focus-trap attribute in HTML:\n%s", html)
	}
}

func TestBindTextRendersDataAttribute(t *testing.T) {
	el := bind.BindText(button.Text("0"), "count")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-bind-text="count"`) {
		t.Errorf("expected data-poly-bind-text attribute in HTML:\n%s", html)
	}
}

func TestBindShowRendersDataAttribute(t *testing.T) {
	el := bind.BindShow(button.Text("Panel"), "isOpen")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-bind-show="isOpen"`) {
		t.Errorf("expected data-poly-bind-show attribute in HTML:\n%s", html)
	}
}

func TestBindHideRendersDataAttribute(t *testing.T) {
	el := bind.BindHide(button.Text("Spinner"), "isLoaded")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-bind-hide="isLoaded"`) {
		t.Errorf("expected data-poly-bind-hide attribute in HTML:\n%s", html)
	}
}

func TestBindClassRendersDataAttribute(t *testing.T) {
	el := bind.BindClass(button.Text("Tab"), "active", "isSelected")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-bind-class="active isSelected"`) {
		t.Errorf("expected data-poly-bind-class attribute in HTML:\n%s", html)
	}
}

func TestBindAttrRendersDataAttribute(t *testing.T) {
	el := bind.BindAttr(button.Text("Save"), "disabled", "isSaving")
	html := string(el.Render())

	if !strings.Contains(html, `data-poly-bind-attr="disabled isSaving"`) {
		t.Errorf("expected data-poly-bind-attr attribute in HTML:\n%s", html)
	}
}

func TestClickChains(t *testing.T) {
	// Verify the return type preserves chainability
	el := bind.Click(button.Text("+"), "increment").
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
