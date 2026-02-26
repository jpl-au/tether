package bind_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jpl-au/fluent-poly/bind"
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
		`data-poly-click="delete"`,
		`data-poly-confirm="Are you sure?"`,
		`data-poly-disable="Deleting..."`,
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
		{"OnClick", bind.OnClick("act"), `data-poly-click="act"`},
		{"OnSubmit", bind.OnSubmit("act"), `data-poly-submit="act"`},
		{"OnInput", bind.OnInput("act"), `data-poly-input="act"`},
		{"OnChange", bind.OnChange("act"), `data-poly-change="act"`},
		{"OnKeyDown", bind.OnKeyDown("act"), `data-poly-keydown="act"`},
		{"OnFocus", bind.OnFocus("act"), `data-poly-focus="act"`},
		{"OnBlur", bind.OnBlur("act"), `data-poly-blur="act"`},
		{"OnViewport", bind.OnViewport("act"), `data-poly-viewport="act"`},
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
		bind.WithPreserve(),
		bind.WithFocusTrap(),
		bind.WithIndicator("#spin"),
	)
	html := string(el.Render())

	for _, want := range []string{
		`data-poly-autofocus`,
		`data-poly-preserve`,
		`data-poly-focus-trap`,
		`data-poly-indicator="#spin"`,
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
	if !strings.Contains(html, `data-poly-debounce="150"`) {
		t.Errorf("missing debounce in:\n%s", html)
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
		`data-poly-cloak`,
		`data-poly-permanent`,
		`data-poly-hook="chart"`,
		`data-poly-transition="fade"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
}

func TestApplyNoOptions(t *testing.T) {
	el := bind.Apply(button.Text("plain"))
	html := string(el.Render())
	if strings.Contains(html, "data-poly") {
		t.Errorf("expected no poly attributes in:\n%s", html)
	}
}
