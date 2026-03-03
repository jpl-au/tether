package bind_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jpl-au/fluent-tether/bind"
	"github.com/jpl-au/fluent/html5/button"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/input"
)

func TestApplyMultipleOptions(t *testing.T) {
	el := bind.Apply(button.Text("Delete"),
		bind.OnClick("delete"),
		bind.WithConfirm("Are you sure?"),
		bind.WithDisable("Deleting..."),
	)
	html := string(el.Render())

	for _, want := range []string{
		`data-tether-click="delete"`,
		`data-tether-confirm="Are you sure?"`,
		`data-tether-disable="Deleting..."`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
}

func TestApplyEventOptions(t *testing.T) {
	tests := []struct {
		name string
		opt  bind.Option
		attr string
	}{
		{"OnClick", bind.OnClick("act"), `data-tether-click="act"`},
		{"OnSubmit", bind.OnSubmit("act"), `data-tether-submit="act"`},
		{"OnInput", bind.OnInput("act"), `data-tether-input="act"`},
		{"OnChange", bind.OnChange("act"), `data-tether-change="act"`},
		{"OnKeyDown", bind.OnKeyDown("act"), `data-tether-keydown="act"`},
		{"OnFocus", bind.OnFocus("act"), `data-tether-focus="act"`},
		{"OnBlur", bind.OnBlur("act"), `data-tether-blur="act"`},
		{"OnViewport", bind.OnViewport("act"), `data-tether-viewport="act"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := string(bind.Apply(button.Text("x"), tt.opt).Render())
			if !strings.Contains(html, tt.attr) {
				t.Errorf("missing %s in:\n%s", tt.attr, html)
			}
		})
	}
}

func TestApplyControlOptions(t *testing.T) {
	el := bind.Apply(input.Text("name", ""),
		bind.WithAutoFocus(),
		bind.WithReset(),
		bind.WithFocusTrap(),
		bind.WithIndicator("#spin"),
	)
	html := string(el.Render())

	for _, want := range []string{
		`data-tether-autofocus`,
		`data-tether-reset`,
		`data-tether-focus-trap`,
		`data-tether-indicator="#spin"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
}

func TestApplyTimingOptions(t *testing.T) {
	el := bind.Apply(input.Text("q", ""),
		bind.OnInput("search"),
		bind.WithDebounce(150*time.Millisecond),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-debounce="150"`) {
		t.Errorf("missing debounce in:\n%s", html)
	}
}

func TestWithFilterKeyMatchesFilterKey(t *testing.T) {
	// WithFilterKey must produce the same attribute as bind.FilterKey.
	nested := string(bind.FilterKey(input.Text("q", ""), "Enter").Render())
	applied := string(bind.Apply(input.Text("q", ""), bind.WithFilterKey("Enter")).Render())
	want := `data-tether-key="Enter"`
	if !strings.Contains(nested, want) {
		t.Errorf("FilterKey missing %s in:\n%s", want, nested)
	}
	if !strings.Contains(applied, want) {
		t.Errorf("WithFilterKey missing %s in:\n%s", want, applied)
	}
}

func TestWithData(t *testing.T) {
	el := bind.Apply(div.New(), bind.WithData("tether-custom", "value"))
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-custom="value"`) {
		t.Errorf("missing custom attribute in:\n%s", html)
	}
}

func TestApplyDirectiveOptions(t *testing.T) {
	el := bind.Apply(div.New(),
		bind.WithCloak(),
		bind.WithPermanent(),
		bind.WithHook("chart"),
		bind.WithTransition("fade"),
	)
	html := string(el.Render())

	for _, want := range []string{
		`data-tether-cloak`,
		`data-tether-permanent`,
		`data-tether-hook="chart"`,
		`data-tether-transition="fade"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
}

func TestApplySignalBindingOptions(t *testing.T) {
	tests := []struct {
		name string
		opt  bind.Option
		attr string
	}{
		{"WithBindText", bind.WithBindText("count"), `data-tether-bind-text="count"`},
		{"WithBindShow", bind.WithBindShow("isOpen"), `data-tether-bind-show="isOpen"`},
		{"WithBindHide", bind.WithBindHide("isHidden"), `data-tether-bind-hide="isHidden"`},
		{"WithBindClass", bind.WithBindClass("active", "isSelected"), `data-tether-bind-class="active isSelected"`},
		{"WithBindAttr", bind.WithBindAttr("disabled", "isLoading"), `data-tether-bind-attr="disabled isLoading"`},
		{"WithBindValue", bind.WithBindValue("email"), `data-tether-bind-value="email"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := string(bind.Apply(div.New(), tt.opt).Render())
			if !strings.Contains(html, tt.attr) {
				t.Errorf("missing %s in:\n%s", tt.attr, html)
			}
		})
	}
}

func TestApplySignalDirectiveOptions(t *testing.T) {
	tests := []struct {
		name string
		opt  bind.Option
		attr string
	}{
		{"WithToggleSignal", bind.WithToggleSignal("menuOpen"), `data-tether-toggle-signal="menuOpen"`},
		{"WithSetSignal", bind.WithSetSignal("tab", "settings"), `data-tether-set-signal="tab settings"`},
		{"WithOptimistic", bind.WithOptimistic("liked", "true"), `data-tether-optimistic="liked true"`},
		{"WithOptimisticToggle", bind.WithOptimisticToggle("liked"), `data-tether-optimistic-toggle="liked"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := string(bind.Apply(button.Text("x"), tt.opt).Render())
			if !strings.Contains(html, tt.attr) {
				t.Errorf("missing %s in:\n%s", tt.attr, html)
			}
		})
	}
}

func TestApplyUploadOptions(t *testing.T) {
	el := bind.Apply(button.Text("Upload"),
		bind.WithUpload("avatar"),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-upload="avatar"`) {
		t.Errorf("missing upload attribute in:\n%s", html)
	}
}

func TestApplyUploadProgressOption(t *testing.T) {
	el := bind.Apply(div.New(),
		bind.WithUploadProgress("avatar"),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-bind-attr="value upload:avatar:progress"`) {
		t.Errorf("missing upload progress attribute in:\n%s", html)
	}
}

func TestApplyCompositionWithSignalBindings(t *testing.T) {
	el := bind.Apply(button.Text("Like"),
		bind.OnClick("like"),
		bind.WithDisable("Liking..."),
		bind.WithBindShow("isLiked"),
		bind.WithOptimisticToggle("liked"),
	)
	html := string(el.Render())

	for _, want := range []string{
		`data-tether-click="like"`,
		`data-tether-disable="Liking..."`,
		`data-tether-bind-show="isLiked"`,
		`data-tether-optimistic-toggle="liked"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
}

func TestApplyNoOptions(t *testing.T) {
	el := bind.Apply(button.Text("plain"))
	html := string(el.Render())
	if strings.Contains(html, "data-tether") {
		t.Errorf("expected no tether attributes in:\n%s", html)
	}
}

func TestOnArbitraryEvent(t *testing.T) {
	el := bind.On(div.New(), "dblclick", "open-editor")
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-dblclick="open-editor"`) {
		t.Errorf("missing dblclick attribute in:\n%s", html)
	}
}

func TestWithEventOption(t *testing.T) {
	el := bind.Apply(div.New(), bind.WithEvent("mouseover", "hover"))
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-mouseover="hover"`) {
		t.Errorf("missing mouseover attribute in:\n%s", html)
	}
}

func TestWithCollect(t *testing.T) {
	el := bind.Apply(button.Text("Send"),
		bind.OnClick("send"),
		bind.WithCollect("#message-input"),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-collect="#message-input"`) {
		t.Errorf("missing collect attribute in:\n%s", html)
	}
}

func TestCollect(t *testing.T) {
	el := bind.Collect(bind.Click(button.Text("Send"), "send"), "#message-input")
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-click="send"`) {
		t.Errorf("missing click attribute in:\n%s", html)
	}
	if !strings.Contains(html, `data-tether-collect="#message-input"`) {
		t.Errorf("missing collect attribute in:\n%s", html)
	}
}

func TestCollectMultipleSelectors(t *testing.T) {
	el := bind.Collect(bind.Click(button.Text("Go"), "search"), "#query, #filter")
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-collect="#query, #filter"`) {
		t.Errorf("missing collect attribute in:\n%s", html)
	}
}
