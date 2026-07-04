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
	html := string(el.RenderBytes())

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
			html := string(bind.Apply(button.Text("x"), tt.opt).RenderBytes())
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
	html := string(el.RenderBytes())

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
	html := string(el.RenderBytes())
	if !strings.Contains(html, `data-tether-debounce="150"`) {
		t.Errorf("missing debounce in:\n%s", html)
	}
}

func TestFilterKey(t *testing.T) {
	html := string(bind.Apply(input.Text("q", ""), bind.FilterKey("Enter")).RenderBytes())
	// FilterKey is tether's keyboard filter, named after the option
	// that sets it - unrelated to data-fluent-key, the diff engine's
	// element identity.
	want := `data-tether-filterkey="Enter"`
	if !strings.Contains(html, want) {
		t.Errorf("FilterKey missing %s in:\n%s", want, html)
	}
}

func TestData(t *testing.T) {
	el := bind.Apply(div.New(), bind.Data("tether-custom", "value"))
	html := string(el.RenderBytes())
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
	html := string(el.RenderBytes())

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
		{"Text", bind.Text("count"), `data-tether-bind-text="count"`},
		{"Show", bind.Show("isOpen"), `data-tether-bind-show="isOpen"`},
		{"Hide", bind.Hide("isHidden"), `data-tether-bind-hide="isHidden"`},
		{"Class", bind.Class("active", "isSelected"), `data-tether-bind-class="active isSelected"`},
		{"Attr", bind.Attr("disabled", "isLoading"), `data-tether-bind-attr="disabled isLoading"`},
		{"Value", bind.Value("email"), `data-tether-bind-value="email"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := string(bind.Apply(div.New(), tt.opt).RenderBytes())
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
			html := string(bind.Apply(button.Text("x"), tt.opt).RenderBytes())
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
	html := string(el.RenderBytes())
	if !strings.Contains(html, `data-tether-upload="avatar"`) {
		t.Errorf("missing upload attribute in:\n%s", html)
	}
}

func TestApplyUploadProgressOption(t *testing.T) {
	el := bind.Apply(div.New(),
		bind.UploadProgress("avatar"),
	)
	html := string(el.RenderBytes())
	if !strings.Contains(html, `data-tether-bind-attr="value upload:avatar:progress"`) {
		t.Errorf("missing upload progress attribute in:\n%s", html)
	}
}

func TestApplyCompositionWithSignalBindings(t *testing.T) {
	el := bind.Apply(button.Text("Like"),
		bind.OnClick("like"),
		bind.Disable("Liking..."),
		bind.Show("isLiked"),
		bind.OptimisticToggle("liked"),
	)
	html := string(el.RenderBytes())

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
	html := string(el.RenderBytes())
	if strings.Contains(html, "data-tether") {
		t.Errorf("expected no tether attributes in:\n%s", html)
	}
}

func TestEventArbitrary(t *testing.T) {
	el := bind.Apply(div.New(), bind.Event("dblclick", "open-editor"))
	html := string(el.RenderBytes())
	if !strings.Contains(html, `data-tether-dblclick="open-editor"`) {
		t.Errorf("missing dblclick attribute in:\n%s", html)
	}
}

func TestEventOption(t *testing.T) {
	el := bind.Apply(div.New(), bind.Event("mouseover", "hover"))
	html := string(el.RenderBytes())
	if !strings.Contains(html, `data-tether-mouseover="hover"`) {
		t.Errorf("missing mouseover attribute in:\n%s", html)
	}
}

func TestCollect(t *testing.T) {
	el := bind.Apply(button.Text("Send"),
		bind.OnClick("send"),
		bind.Collect("#message-input"),
	)
	html := string(el.RenderBytes())
	if !strings.Contains(html, `data-tether-collect="#message-input"`) {
		t.Errorf("missing collect attribute in:\n%s", html)
	}
}

func TestCollectMultipleSelectors(t *testing.T) {
	el := bind.Apply(button.Text("Go"),
		bind.OnClick("search"),
		bind.Collect("#query, #filter"),
	)
	html := string(el.RenderBytes())
	if !strings.Contains(html, `data-tether-collect="#query, #filter"`) {
		t.Errorf("missing collect attribute in:\n%s", html)
	}
}

func TestPreventDefault(t *testing.T) {
	el := bind.Apply(div.New(),
		bind.Event("contextmenu", "menu.open"),
		bind.PreventDefault(),
	)
	html := string(el.RenderBytes())
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
	html := string(bind.Apply(button.Text("Copy"), bind.CopyToClipboard("#key")).RenderBytes())
	if !strings.Contains(html, `data-tether-copy="#key"`) {
		t.Errorf("missing copy attribute in:\n%s", html)
	}
}

func TestHotkey(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Hotkey("ctrl+k", "search.open")).RenderBytes())
	if !strings.Contains(html, `data-tether-hotkey="ctrl-k search.open"`) {
		t.Errorf("missing hotkey attribute in:\n%s", html)
	}
}

func TestHotkeyNormalisesPlus(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Hotkey("ctrl+shift+p", "palette")).RenderBytes())
	if !strings.Contains(html, `data-tether-hotkey="ctrl-shift-p palette"`) {
		t.Errorf("missing normalised hotkey in:\n%s", html)
	}
}

func TestHotkeySpecialCharacters(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Hotkey("ctrl+/", "help")).RenderBytes())
	if !strings.Contains(html, `data-tether-hotkey="ctrl-/ help"`) {
		t.Errorf("missing hotkey with slash in:\n%s", html)
	}

	html = string(bind.Apply(div.New(), bind.Hotkey("shift+?", "search")).RenderBytes())
	if !strings.Contains(html, `data-tether-hotkey="shift-? search"`) {
		t.Errorf("missing hotkey with question mark in:\n%s", html)
	}
}

func TestDraggable(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Draggable()).RenderBytes())
	if !strings.Contains(html, `data-tether-draggable`) {
		t.Errorf("missing draggable attribute in:\n%s", html)
	}
}

func TestDropTarget(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.DropTarget("card.move")).RenderBytes())
	if !strings.Contains(html, `data-tether-drop-target="card.move"`) {
		t.Errorf("missing drop-target attribute in:\n%s", html)
	}
}

func TestSortable(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Sortable("card.reorder")).RenderBytes())
	if !strings.Contains(html, `data-tether-sortable="card.reorder"`) {
		t.Errorf("missing sortable attribute in:\n%s", html)
	}
}

func TestScrollToClient(t *testing.T) {
	html := string(bind.Apply(button.Text("Top"), bind.ScrollTo("#top")).RenderBytes())
	if !strings.Contains(html, `data-tether-scroll-to="#top"`) {
		t.Errorf("missing scroll-to attribute in:\n%s", html)
	}
}

func TestPreserveScroll(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.PreserveScroll()).RenderBytes())
	if !strings.Contains(html, `data-tether-preserve-scroll`) {
		t.Errorf("missing preserve-scroll attribute in:\n%s", html)
	}
}

func TestRequired(t *testing.T) {
	html := string(bind.Apply(input.Text("name", ""), bind.Required("Name is required")).RenderBytes())
	if !strings.Contains(html, `data-tether-required="Name is required"`) {
		t.Errorf("missing required attribute in:\n%s", html)
	}
}

func TestMinLength(t *testing.T) {
	html := string(bind.Apply(input.Text("pw", ""), bind.MinLength(8, "At least 8 characters")).RenderBytes())
	if !strings.Contains(html, `data-tether-minlength="8 At least 8 characters"`) {
		t.Errorf("missing minlength attribute in:\n%s", html)
	}
}

func TestMaxLength(t *testing.T) {
	html := string(bind.Apply(input.Text("bio", ""), bind.MaxLength(140, "Too long")).RenderBytes())
	if !strings.Contains(html, `data-tether-maxlength="140 Too long"`) {
		t.Errorf("missing maxlength attribute in:\n%s", html)
	}
}

func TestPattern(t *testing.T) {
	html := string(bind.Apply(input.Text("email", ""), bind.Pattern("[^@]+@[^@]+", "Invalid email")).RenderBytes())
	if !strings.Contains(html, `data-tether-pattern="[^@]+@[^@]+ Invalid email"`) {
		t.Errorf("missing pattern attribute in:\n%s", html)
	}
}

func TestEditable(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Editable("item.rename")).RenderBytes())
	if !strings.Contains(html, `data-tether-editable="item.rename"`) {
		t.Errorf("missing editable attribute in:\n%s", html)
	}
}

func TestSelectable(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Selectable()).RenderBytes())
	if !strings.Contains(html, `data-tether-selectable`) {
		t.Errorf("missing selectable attribute in:\n%s", html)
	}
}

func TestCollectSelected(t *testing.T) {
	html := string(bind.Apply(button.Text("Delete"), bind.OnClick("del"), bind.CollectSelected("#list")).RenderBytes())
	if !strings.Contains(html, `data-tether-collect-selected="#list"`) {
		t.Errorf("missing collect-selected attribute in:\n%s", html)
	}
}

func TestOnSwipe(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.OnSwipe("card.dismiss")).RenderBytes())
	if !strings.Contains(html, `data-tether-swipe="card.dismiss"`) {
		t.Errorf("missing swipe attribute in:\n%s", html)
	}
}

func TestOnLongPress(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.OnLongPress("item.menu")).RenderBytes())
	if !strings.Contains(html, `data-tether-longpress="item.menu"`) {
		t.Errorf("missing longpress attribute in:\n%s", html)
	}
}

func TestConditionalBindings(t *testing.T) {
	tests := []struct {
		name string
		opt  bind.Option
		attr string
	}{
		{"ShowWhen int", bind.ShowWhen("count", ">", 5), `data-tether-bind-show-when="count > 5"`},
		{"HideWhen str", bind.HideWhen("status", "==", "done"), `data-tether-bind-hide-when="status == done"`},
		{"ClassWhen", bind.ClassWhen("danger", "seconds", "<", 10), `data-tether-bind-class-when="danger seconds < 10"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := string(bind.Apply(div.New(), tt.opt).RenderBytes())
			if !strings.Contains(html, tt.attr) {
				t.Errorf("missing %s in:\n%s", tt.attr, html)
			}
		})
	}
}

func TestConditionalBindingUnknownOpPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on unknown operator")
		}
	}()
	bind.ShowWhen("count", "=<", 5)
}

func TestEmit(t *testing.T) {
	html := string(bind.Apply(button.Text("Clear"), bind.Emit("clear", "#search")).RenderBytes())
	if !strings.Contains(html, `data-tether-emit="clear #search"`) {
		t.Errorf("missing emit attribute in:\n%s", html)
	}
}

func TestOnClientEvent(t *testing.T) {
	tests := []struct {
		name string
		opt  bind.Option
		attr string
	}{
		{"set", bind.OnClientEvent("clear", bind.SetSignal("query", "")), `data-tether-on-clear="set-signal query "`},
		{"toggle", bind.OnClientEvent("flip", bind.ToggleSignal("open")), `data-tether-on-flip="toggle-signal open"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := string(bind.Apply(input.Text("q", ""), tt.opt).RenderBytes())
			if !strings.Contains(html, tt.attr) {
				t.Errorf("missing %s in:\n%s", tt.attr, html)
			}
		})
	}
}

func TestOnClientEventRejectsNonSignalAction(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on non-signal action")
		}
	}()
	bind.OnClientEvent("clear", bind.OnClick("send"))
}

func TestFilter(t *testing.T) {
	html := string(bind.Apply(input.Text("q", ""), bind.Filter("#item-list")).RenderBytes())
	if !strings.Contains(html, `data-tether-filter="#item-list"`) {
		t.Errorf("missing filter attribute in:\n%s", html)
	}
}

func TestFilterItem(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.FilterItem()).RenderBytes())
	if !strings.Contains(html, `data-tether-filter-item`) {
		t.Errorf("missing filter-item attribute in:\n%s", html)
	}
}

func TestTemplate(t *testing.T) {
	html := string(bind.Apply(div.New(), bind.Template("people", "#people-list")).RenderBytes())
	if !strings.Contains(html, `data-tether-template="people #people-list"`) {
		t.Errorf("missing template attribute in:\n%s", html)
	}
}
