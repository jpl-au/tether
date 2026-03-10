package tether_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jpl-au/tether/bind"
	"github.com/jpl-au/fluent/html5/a"
	"github.com/jpl-au/fluent/html5/button"
	"github.com/jpl-au/fluent/html5/dropdown"
	"github.com/jpl-au/fluent/html5/form"
	"github.com/jpl-au/fluent/html5/input"
)

func TestClickRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("+"), bind.OnClick("increment"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-click="increment"`) {
		t.Errorf("expected data-tether-click attribute in HTML:\n%s", html)
	}
}

func TestSubmitRendersDataAttribute(t *testing.T) {
	el := bind.Apply(form.New(), bind.OnSubmit("save"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-submit="save"`) {
		t.Errorf("expected data-tether-submit attribute in HTML:\n%s", html)
	}
}

func TestInputRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("name", ""), bind.OnInput("update"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-input="update"`) {
		t.Errorf("expected data-tether-input attribute in HTML:\n%s", html)
	}
}

func TestLinkRendersDataAttribute(t *testing.T) {
	el := bind.Apply(a.Link("/profile", "Profile"), bind.Link())
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-link=""`) {
		t.Errorf("expected data-tether-link attribute in HTML:\n%s", html)
	}
	if !strings.Contains(html, `href="/profile"`) {
		t.Errorf("expected href attribute in HTML:\n%s", html)
	}
}

func TestToggleClassRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Menu"), bind.ToggleClass("is-open"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-toggle-class="is-open"`) {
		t.Errorf("expected data-tether-toggle-class attribute in HTML:\n%s", html)
	}
}

func TestToggleTargetRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Menu"), bind.ToggleTarget("#nav"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-toggle-target="#nav"`) {
		t.Errorf("expected data-tether-toggle-target attribute in HTML:\n%s", html)
	}
}

func TestToggleAttrRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Toggle"), bind.ToggleAttr("hidden"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-toggle-attr="hidden"`) {
		t.Errorf("expected data-tether-toggle-attr attribute in HTML:\n%s", html)
	}
}

func TestToggleClassWithTargetChains(t *testing.T) {
	el := bind.Apply(button.Text("Menu"),
		bind.ToggleTarget("#nav"),
		bind.ToggleClass("is-open"),
	).Style("cursor: pointer")
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-toggle-class="is-open"`) {
		t.Errorf("missing data-tether-toggle-class in HTML:\n%s", html)
	}
	if !strings.Contains(html, `data-tether-toggle-target="#nav"`) {
		t.Errorf("missing data-tether-toggle-target in HTML:\n%s", html)
	}
	if !strings.Contains(html, `style="cursor: pointer"`) {
		t.Errorf("missing style in HTML:\n%s", html)
	}
}

func TestTransitionRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Item"), bind.Transition("fade"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-transition="fade"`) {
		t.Errorf("expected data-tether-transition attribute in HTML:\n%s", html)
	}
}

func TestResetRendersDataAttribute(t *testing.T) {
	el := bind.Apply(form.New(), bind.Reset())
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-reset=""`) {
		t.Errorf("expected data-tether-reset attribute in HTML:\n%s", html)
	}
}

func TestAutoFocusRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("name", ""), bind.AutoFocus())
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-autofocus=""`) {
		t.Errorf("expected data-tether-autofocus attribute in HTML:\n%s", html)
	}
}

func TestHookRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Chart"), bind.Hook("chart"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-hook="chart"`) {
		t.Errorf("expected data-tether-hook attribute in HTML:\n%s", html)
	}
}

func TestDisableRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Save"),
		bind.OnClick("save"),
		bind.Disable("Saving..."),
	)
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-disable="Saving..."`) {
		t.Errorf("expected data-tether-disable attribute in HTML:\n%s", html)
	}
}

func TestDisableEmptyTextRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Go"),
		bind.OnClick("go"),
		bind.Disable(""),
	)
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-disable=""`) {
		t.Errorf("expected data-tether-disable attribute in HTML:\n%s", html)
	}
}

func TestConfirmRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Delete"),
		bind.OnClick("delete"),
		bind.Confirm("Are you sure?"),
	)
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-confirm="Are you sure?"`) {
		t.Errorf("expected data-tether-confirm attribute in HTML:\n%s", html)
	}
}

func TestDebounceRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("q", ""),
		bind.OnInput("search"),
		bind.Debounce(500*time.Millisecond),
	)
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-debounce="500"`) {
		t.Errorf("expected data-tether-debounce attribute in HTML:\n%s", html)
	}
}

func TestThrottleRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Go"),
		bind.OnClick("fire"),
		bind.Throttle(1*time.Second),
	)
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-throttle="1000"`) {
		t.Errorf("expected data-tether-throttle attribute in HTML:\n%s", html)
	}
}

func TestViewportRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Sentinel"), bind.OnViewport("load-more"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-viewport="load-more"`) {
		t.Errorf("expected data-tether-viewport attribute in HTML:\n%s", html)
	}
}

func TestChangeRendersDataAttribute(t *testing.T) {
	el := bind.Apply(dropdown.New(), bind.OnChange("filter"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-change="filter"`) {
		t.Errorf("expected data-tether-change attribute in HTML:\n%s", html)
	}
}

func TestKeyDownRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("cmd", ""), bind.OnKeyDown("exec"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-keydown="exec"`) {
		t.Errorf("expected data-tether-keydown attribute in HTML:\n%s", html)
	}
}

func TestFocusRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("name", ""), bind.OnFocus("focus-name"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-focus="focus-name"`) {
		t.Errorf("expected data-tether-focus attribute in HTML:\n%s", html)
	}
}

func TestBlurRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("name", ""), bind.OnBlur("blur-name"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-blur="blur-name"`) {
		t.Errorf("expected data-tether-blur attribute in HTML:\n%s", html)
	}
}

func TestFilterKeyRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("cmd", ""),
		bind.OnKeyDown("exec"),
		bind.FilterKey("Enter"),
	)
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-key="Enter"`) {
		t.Errorf("expected data-tether-key attribute in HTML:\n%s", html)
	}
	if !strings.Contains(html, `data-tether-keydown="exec"`) {
		t.Errorf("expected data-tether-keydown attribute in HTML:\n%s", html)
	}
}

func TestFocusTrapRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Dialog"), bind.FocusTrap())
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-focus-trap=""`) {
		t.Errorf("expected data-tether-focus-trap attribute in HTML:\n%s", html)
	}
}

func TestBindTextRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("0"), bind.BindText("count"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-bind-text="count"`) {
		t.Errorf("expected data-tether-bind-text attribute in HTML:\n%s", html)
	}
}

func TestBindShowRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Panel"), bind.BindShow("isOpen"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-bind-show="isOpen"`) {
		t.Errorf("expected data-tether-bind-show attribute in HTML:\n%s", html)
	}
}

func TestBindHideRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Spinner"), bind.BindHide("isLoaded"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-bind-hide="isLoaded"`) {
		t.Errorf("expected data-tether-bind-hide attribute in HTML:\n%s", html)
	}
}

func TestBindClassRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Tab"), bind.BindClass("active", "isSelected"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-bind-class="active isSelected"`) {
		t.Errorf("expected data-tether-bind-class attribute in HTML:\n%s", html)
	}
}

func TestBindAttrRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Save"), bind.BindAttr("disabled", "isSaving"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-bind-attr="disabled isSaving"`) {
		t.Errorf("expected data-tether-bind-attr attribute in HTML:\n%s", html)
	}
}

func TestBindValueRendersDataAttribute(t *testing.T) {
	el := bind.Apply(input.Text("email", ""), bind.BindValue("email"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-bind-value="email"`) {
		t.Errorf("expected data-tether-bind-value attribute in HTML:\n%s", html)
	}
}

func TestIndicatorRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Save"),
		bind.OnClick("save"),
		bind.Indicator("#spinner"),
	)
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-indicator="#spinner"`) {
		t.Errorf("expected data-tether-indicator attribute in HTML:\n%s", html)
	}
}

func TestCloakRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Hidden"), bind.Cloak())
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-cloak=""`) {
		t.Errorf("expected data-tether-cloak attribute in HTML:\n%s", html)
	}
}

func TestPermanentRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Video"), bind.Permanent())
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-permanent=""`) {
		t.Errorf("expected data-tether-permanent attribute in HTML:\n%s", html)
	}
}

func TestToggleSignalRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Menu"), bind.ToggleSignal("menuOpen"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-toggle-signal="menuOpen"`) {
		t.Errorf("expected data-tether-toggle-signal attribute in HTML:\n%s", html)
	}
}

func TestSetSignalRendersDataAttribute(t *testing.T) {
	el := bind.Apply(button.Text("Settings"), bind.SetSignal("tab", "settings"))
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-set-signal="tab settings"`) {
		t.Errorf("expected data-tether-set-signal attribute in HTML:\n%s", html)
	}
}

func TestApplyChains(t *testing.T) {
	el := bind.Apply(button.Text("+"), bind.OnClick("increment")).
		Style("cursor: pointer").
		Class("btn")
	html := string(el.Render())

	if !strings.Contains(html, `data-tether-click="increment"`) {
		t.Errorf("missing data-tether-click in HTML:\n%s", html)
	}
	if !strings.Contains(html, `style="cursor: pointer"`) {
		t.Errorf("missing style in HTML:\n%s", html)
	}
	if !strings.Contains(html, `class="btn"`) {
		t.Errorf("missing class in HTML:\n%s", html)
	}
}
