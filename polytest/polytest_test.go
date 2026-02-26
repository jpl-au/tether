package polytest_test

import (
	"testing"

	poly "github.com/jpl-au/fluent-poly"
	"github.com/jpl-au/fluent-poly/polytest"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

type state struct {
	Count int
	Name  string
}

func render(s state) node.Node {
	return div.New(
		span.Textf("Count: %d", s.Count).Dynamic("count"),
	)
}

func handle(sess poly.PreSession, s state, ev poly.Event) state {
	switch ev.Action {
	case "increment":
		s.Count++
	case "decrement":
		s.Count--
	case "toast":
		sess.Toast("hello")
	case "navigate":
		sess.Navigate("/other")
	case "title":
		sess.SetTitle("New Title")
	case "announce":
		sess.Announce("done")
	case "flash":
		sess.Flash("#msg", "saved")
	case "signal":
		sess.Signal("count", s.Count)
	case "set-name":
		s.Name = ev.Value()
	}
	return s
}

func newHarness() *polytest.Harness[state] {
	return polytest.New(polytest.Config[state]{
		State:  state{Count: 0},
		Render: render,
		Handle: handle,
	})
}

func TestSendUpdatesState(t *testing.T) {
	h := newHarness()

	h.Send("increment")
	if h.State().Count != 1 {
		t.Errorf("Count = %d, want 1", h.State().Count)
	}

	h.Send("increment")
	if h.State().Count != 2 {
		t.Errorf("Count = %d, want 2", h.State().Count)
	}

	h.Send("decrement")
	if h.State().Count != 1 {
		t.Errorf("Count = %d, want 1", h.State().Count)
	}
}

func TestHTMLContainsRenderedContent(t *testing.T) {
	h := newHarness()
	h.Send("increment")

	html := h.HTML()
	if html == "" {
		t.Fatal("HTML() returned empty string")
	}
	if !contains(html, "Count: 1") {
		t.Errorf("HTML should contain Count: 1, got %s", html)
	}
}

func TestToast(t *testing.T) {
	h := newHarness()
	h.Send("toast")

	if !h.HasToast("hello") {
		t.Errorf("Toast() = %q, want %q", h.Toast(), "hello")
	}
}

func TestNavigate(t *testing.T) {
	h := newHarness()
	h.Send("navigate")

	if h.URL() != "/other" {
		t.Errorf("URL() = %q, want %q", h.URL(), "/other")
	}
}

func TestTitle(t *testing.T) {
	h := newHarness()
	h.Send("title")

	if h.Title() != "New Title" {
		t.Errorf("Title() = %q, want %q", h.Title(), "New Title")
	}
}

func TestAnnounce(t *testing.T) {
	h := newHarness()
	h.Send("announce")

	if h.Announce() != "done" {
		t.Errorf("Announce() = %q, want %q", h.Announce(), "done")
	}
}

func TestFlash(t *testing.T) {
	h := newHarness()
	h.Send("flash")

	flash := h.Flash()
	if flash == nil || flash["#msg"] != "saved" {
		t.Errorf("Flash() = %v, want {#msg: saved}", flash)
	}
}

func TestSendInput(t *testing.T) {
	h := newHarness()
	h.SendInput("set-name", "Alice")

	if h.State().Name != "Alice" {
		t.Errorf("Name = %q, want %q", h.State().Name, "Alice")
	}
}

func TestSendSubmit(t *testing.T) {
	h := polytest.New(polytest.Config[state]{
		State:  state{},
		Render: render,
		Handle: func(_ poly.PreSession, s state, ev poly.Event) state {
			s.Name = ev.Data["name"]
			return s
		},
	})

	h.SendSubmit("save", map[string]string{"name": "Bob"})
	if h.State().Name != "Bob" {
		t.Errorf("Name = %q, want %q", h.State().Name, "Bob")
	}
}

func TestRender(t *testing.T) {
	h := newHarness()
	html := h.Render()

	if !contains(html, "Count: 0") {
		t.Errorf("Render() should contain Count: 0, got %s", html)
	}
}

func TestRenderNode(t *testing.T) {
	h := newHarness()
	n := h.RenderNode()

	if n == nil {
		t.Fatal("RenderNode() returned nil")
	}
	html := string(n.Render())
	if !contains(html, "Count: 0") {
		t.Errorf("RenderNode HTML should contain Count: 0, got %s", html)
	}
}

func TestEffectsResetBetweenSends(t *testing.T) {
	h := newHarness()

	h.Send("toast")
	if h.Toast() != "hello" {
		t.Fatalf("expected toast after first send")
	}

	h.Send("increment")
	if h.Toast() != "" {
		t.Errorf("Toast() = %q, want empty after non-toast event", h.Toast())
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && // avoid trivial matches
		stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
