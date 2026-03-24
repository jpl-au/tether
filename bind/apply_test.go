package bind_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jpl-au/fluent/html5/button"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/input"
	"github.com/jpl-au/tether/bind"
)

func TestApplyMultipleOptions(t *testing.T) {
	el := bind.Apply(button.Text("Delete"),
		bind.OnClick("delete"),
		bind.Confirm("Are you sure?"),
		bind.Disable("Deleting..."),
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
		{"OnPaste", bind.OnPaste("act"), `data-tether-paste="act"`},
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
		bind.AutoFocus(),
		bind.Reset(),
		bind.FocusTrap(),
		bind.Indicator("#spin"),
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
		bind.Debounce(150*time.Millisecond),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-debounce="150"`) {
		t.Errorf("missing debounce in:\n%s", html)
	}
}

func TestFilterKey(t *testing.T) {
	html := string(bind.Apply(input.Text("q", ""), bind.FilterKey("Enter")).Render())
	want := `data-tether-key="Enter"`
	if !strings.Contains(html, want) {
		t.Errorf("FilterKey missing %s in:\n%s", want, html)
	}
}

func TestData(t *testing.T) {
	el := bind.Apply(div.New(), bind.Data("tether-custom", "value"))
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-custom="value"`) {
		t.Errorf("missing custom attribute in:\n%s", html)
	}
}

func TestApplyDirectiveOptions(t *testing.T) {
	el := bind.Apply(div.New(),
		bind.Cloak(),
		bind.Permanent(),
		bind.Hook("chart"),
		bind.Transition("fade"),
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
		{"BindText", bind.BindText("count"), `data-tether-bind-text="count"`},
		{"BindShow", bind.BindShow("isOpen"), `data-tether-bind-show="isOpen"`},
		{"BindHide", bind.BindHide("isHidden"), `data-tether-bind-hide="isHidden"`},
		{"BindClass", bind.BindClass("active", "isSelected"), `data-tether-bind-class="active isSelected"`},
		{"BindAttr", bind.BindAttr("disabled", "isLoading"), `data-tether-bind-attr="disabled isLoading"`},
		{"BindValue", bind.BindValue("email"), `data-tether-bind-value="email"`},
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
		{"ToggleSignal", bind.ToggleSignal("menuOpen"), `data-tether-toggle-signal="menuOpen"`},
		{"SetSignal", bind.SetSignal("tab", "settings"), `data-tether-set-signal="tab settings"`},
		{"Optimistic", bind.Optimistic("liked", "true"), `data-tether-optimistic="liked true"`},
		{"OptimisticToggle", bind.OptimisticToggle("liked"), `data-tether-optimistic-toggle="liked"`},
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
		bind.Upload("avatar"),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-upload="avatar"`) {
		t.Errorf("missing upload attribute in:\n%s", html)
	}
}

func TestApplyUploadProgressOption(t *testing.T) {
	el := bind.Apply(div.New(),
		bind.UploadProgress("avatar"),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-bind-attr="value upload:avatar:progress"`) {
		t.Errorf("missing upload progress attribute in:\n%s", html)
	}
}

func TestApplyCompositionWithSignalBindings(t *testing.T) {
	el := bind.Apply(button.Text("Like"),
		bind.OnClick("like"),
		bind.Disable("Liking..."),
		bind.BindShow("isLiked"),
		bind.OptimisticToggle("liked"),
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

func TestEventArbitrary(t *testing.T) {
	el := bind.Apply(div.New(), bind.Event("dblclick", "open-editor"))
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-dblclick="open-editor"`) {
		t.Errorf("missing dblclick attribute in:\n%s", html)
	}
}

func TestEventOption(t *testing.T) {
	el := bind.Apply(div.New(), bind.Event("mouseover", "hover"))
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-mouseover="hover"`) {
		t.Errorf("missing mouseover attribute in:\n%s", html)
	}
}

func TestCollect(t *testing.T) {
	el := bind.Apply(button.Text("Send"),
		bind.OnClick("send"),
		bind.Collect("#message-input"),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-collect="#message-input"`) {
		t.Errorf("missing collect attribute in:\n%s", html)
	}
}

func TestCollectMultipleSelectors(t *testing.T) {
	el := bind.Apply(button.Text("Go"),
		bind.OnClick("search"),
		bind.Collect("#query, #filter"),
	)
	html := string(el.Render())
	if !strings.Contains(html, `data-tether-collect="#query, #filter"`) {
		t.Errorf("missing collect attribute in:\n%s", html)
	}
}

func TestPreventDefault(t *testing.T) {
	el := bind.Apply(div.New(),
		bind.Event("contextmenu", "menu.open"),
		bind.PreventDefault(),
	)
	html := string(el.Render())
	for _, want := range []string{
		`data-tether-contextmenu="menu.open"`,
		`data-tether-prevent-default`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
}

func TestCopyToClipboard(t *testing.T) {
	html := string(bind.Apply(button.Text("Copy"), bind.CopyToClipboard("#key")).Render())
	if !strings.Contains(html, `data-tether-copy="#key"`) {
		t.Errorf("missing copy attribute in:\n%s", html)
	}
}

func TestHotkey(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Hotkey("ctrl+k", "search.open")).Render())
	if !strings.Contains(html, `data-tether-hotkey-ctrl-k="search.open"`) {
		t.Errorf("missing hotkey attribute in:\n%s", html)
	}
}

func TestHotkeyNormalisesPlus(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Hotkey("ctrl+shift+p", "palette")).Render())
	if !strings.Contains(html, `data-tether-hotkey-ctrl-shift-p="palette"`) {
		t.Errorf("missing normalised hotkey in:\n%s", html)
	}
}

func TestDraggable(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Draggable()).Render())
	if !strings.Contains(html, `data-tether-draggable`) {
		t.Errorf("missing draggable attribute in:\n%s", html)
	}
}

func TestDropTarget(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.DropTarget("card.move")).Render())
	if !strings.Contains(html, `data-tether-drop-target="card.move"`) {
		t.Errorf("missing drop-target attribute in:\n%s", html)
	}
}

func TestSortable(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Sortable("card.reorder")).Render())
	if !strings.Contains(html, `data-tether-sortable="card.reorder"`) {
		t.Errorf("missing sortable attribute in:\n%s", html)
	}
}

func TestScrollToClient(t *testing.T) {
	html := string(bind.Apply(button.Text("Top"), bind.ScrollTo("#top")).Render())
	if !strings.Contains(html, `data-tether-scroll-to="#top"`) {
		t.Errorf("missing scroll-to attribute in:\n%s", html)
	}
}

func TestPreserveScroll(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.PreserveScroll()).Render())
	if !strings.Contains(html, `data-tether-preserve-scroll`) {
		t.Errorf("missing preserve-scroll attribute in:\n%s", html)
	}
}
