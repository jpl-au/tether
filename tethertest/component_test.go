package tethertest_test

import (
	"testing"

	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	tether "github.com/jpl-au/tether"
	"github.com/jpl-au/tether/tethertest"
)

// counter is a minimal component for testing.
type counter struct {
	Count int
}

func (c counter) Render() node.Node {
	return span.Textf("count:%d", c.Count).Dynamic("count")
}

func (c counter) Handle(sess tether.Session, ev tether.Event) tether.Component {
	switch ev.Action {
	case "inc":
		c.Count++
	case "dec":
		c.Count--
	case "toast":
		sess.Toast("clicked")
	case "signal":
		sess.Signal("count", c.Count)
	case "flash":
		sess.Flash("#msg", "saved")
	case "announce":
		sess.Announce("updated")
	case "navigate":
		sess.Navigate("/next")
	case "title":
		sess.SetTitle("New Title")
	}
	return c
}

func TestComponentSend(t *testing.T) {
	h := tethertest.NewComponent(counter{})

	h.Send("inc")
	if h.Component().Count != 1 {
		t.Errorf("Count = %d, want 1", h.Component().Count)
	}

	h.Send("inc")
	h.Send("inc")
	h.Send("dec")
	if h.Component().Count != 2 {
		t.Errorf("Count = %d, want 2", h.Component().Count)
	}
}

func TestComponentHTML(t *testing.T) {
	h := tethertest.NewComponent(counter{Count: 5})

	html := h.HTML()
	if !contains(html, "count:5") {
		t.Errorf("HTML = %q, want to contain count:5", html)
	}

	h.Send("inc")
	html = h.HTML()
	if !contains(html, "count:6") {
		t.Errorf("HTML = %q, want to contain count:6", html)
	}
}

func TestComponentToast(t *testing.T) {
	h := tethertest.NewComponent(counter{})
	h.Send("toast")

	if !h.HasToast("clicked") {
		t.Errorf("Toast() = %q, want clicked", h.Toast())
	}
}

func TestComponentSignal(t *testing.T) {
	h := tethertest.NewComponent(counter{Count: 3})
	h.Send("signal")

	if !h.HasSignal("count", 3) {
		t.Errorf("HasSignal(count, 3) = false; signals = %v", h.Signals())
	}
}

func TestComponentFlash(t *testing.T) {
	h := tethertest.NewComponent(counter{})
	h.Send("flash")

	if !h.HasFlash("#msg", "saved") {
		t.Errorf("HasFlash(#msg, saved) = false; flash = %v", h.Flash())
	}
}

func TestComponentAnnounce(t *testing.T) {
	h := tethertest.NewComponent(counter{})
	h.Send("announce")

	if !h.HasAnnounce("updated") {
		t.Errorf("HasAnnounce(updated) = false; got %q", h.Announce())
	}
}

func TestComponentNavigate(t *testing.T) {
	h := tethertest.NewComponent(counter{})
	h.Send("navigate")

	if h.URL() != "/next" {
		t.Errorf("URL() = %q, want /next", h.URL())
	}
	if h.Replaced() {
		t.Error("Replaced() = true, want false")
	}
}

func TestComponentTitle(t *testing.T) {
	h := tethertest.NewComponent(counter{})
	h.Send("title")

	if h.Title() != "New Title" {
		t.Errorf("Title() = %q, want New Title", h.Title())
	}
}

func TestComponentEffectsResetBetweenSends(t *testing.T) {
	h := tethertest.NewComponent(counter{})

	h.Send("toast")
	if h.Toast() != "clicked" {
		t.Fatal("expected toast after first send")
	}

	h.Send("inc")
	if h.Toast() != "" {
		t.Errorf("Toast() = %q, want empty after non-toast event", h.Toast())
	}
}

func TestComponentSendInput(t *testing.T) {
	h := tethertest.NewComponent(inputWidget{})
	h.SendInput("set", "hello")

	if h.Component().Value != "hello" {
		t.Errorf("Value = %q, want hello", h.Component().Value)
	}
}

// inputWidget is a component that captures input values.
type inputWidget struct {
	Value string
}

func (w inputWidget) Render() node.Node { return span.Text(w.Value) }
func (w inputWidget) Handle(_ tether.Session, ev tether.Event) tether.Component {
	if ev.Action == "set" {
		w.Value = ev.Value()
	}
	return w
}

// mountable is a component implementing Mounter.
type mountable struct {
	Ready bool
}

func (m mountable) Render() node.Node { return span.Text("ready") }
func (m mountable) Handle(_ tether.Session, _ tether.Event) tether.Component {
	return m
}
func (m mountable) Mount(sess tether.Session) tether.Component {
	sess.Toast("mounted")
	m.Ready = true
	return m
}

func TestComponentMount(t *testing.T) {
	h := tethertest.NewComponent(mountable{})
	h.Mount()

	if !h.Component().Ready {
		t.Error("Ready = false, want true after Mount")
	}
	if !h.HasToast("mounted") {
		t.Errorf("Toast() = %q, want mounted", h.Toast())
	}
}

func TestComponentMountPanicsWithoutMounter(t *testing.T) {
	h := tethertest.NewComponent(counter{})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from Mount on non-Mounter component")
		}
	}()
	h.Mount()
}
