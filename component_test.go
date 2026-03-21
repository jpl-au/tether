package tether

import (
	"testing"

	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// testWidget is a minimal Component implementation for testing.
type testWidget struct {
	prefix string
	Count  int
	Last   string
}

func (w testWidget) Render() node.Node {
	return span.Text(w.Last)
}

func (w testWidget) Handle(sess Session, ev Event) Component {
	switch ev.Action {
	case "inc":
		w.Count++
	case "set":
		w.Last = ev.Value()
	}
	return w
}

func TestRouteMatchingPrefix(t *testing.T) {
	w := testWidget{prefix: "counter", Count: 0}
	sess := &CaptureSession{SessionID: "test"}
	ev := Event{Action: "counter.inc"}

	result := Route(w, "counter", sess, ev)
	got := result.(testWidget)
	if got.Count != 1 {
		t.Errorf("Count = %d, want 1", got.Count)
	}
}

func TestRouteNonMatchingPrefix(t *testing.T) {
	w := testWidget{prefix: "counter", Count: 5}
	sess := &CaptureSession{SessionID: "test"}
	ev := Event{Action: "other.inc"}

	result := Route(w, "counter", sess, ev)
	got := result.(testWidget)
	if got.Count != 5 {
		t.Errorf("Count = %d, want 5 (unchanged)", got.Count)
	}
}

func TestRouteStripsPrefix(t *testing.T) {
	w := testWidget{}
	sess := &CaptureSession{SessionID: "test"}
	ev := Event{Action: "chat.set", Data: map[string]string{"value": "hello"}}

	result := Route(w, "chat", sess, ev)
	got := result.(testWidget)
	if got.Last != "hello" {
		t.Errorf("Last = %q, want hello", got.Last)
	}
}

func TestRouteTypedPreservesConcreteType(t *testing.T) {
	w := testWidget{Count: 0}
	sess := &CaptureSession{SessionID: "test"}
	ev := Event{Action: "w.inc"}

	// RouteTyped returns testWidget, not Component - no type assertion needed.
	got := RouteTyped(w, "w", sess, ev)
	if got.Count != 1 {
		t.Errorf("Count = %d, want 1", got.Count)
	}
	// Verify direct field access works (compile-time check).
	_ = got.Last
}

func TestRouteTypedNonMatchingPrefix(t *testing.T) {
	w := testWidget{Count: 7}
	sess := &CaptureSession{SessionID: "test"}
	ev := Event{Action: "other.inc"}

	got := RouteTyped(w, "w", sess, ev)
	if got.Count != 7 {
		t.Errorf("Count = %d, want 7 (unchanged)", got.Count)
	}
}

func TestRouteNoPartialPrefixMatch(t *testing.T) {
	w := testWidget{Count: 0}
	sess := &CaptureSession{SessionID: "test"}

	// "counter_extra.inc" should NOT match prefix "counter"  -
	// Route requires "counter." as the delimiter.
	ev := Event{Action: "counter_extra.inc"}
	got := RouteTyped(w, "counter", sess, ev)
	if got.Count != 0 {
		t.Errorf("Count = %d, want 0 (partial prefix should not match)", got.Count)
	}
}

func TestRouteSessionSideEffects(t *testing.T) {
	w := toastWidget{}
	sess := &CaptureSession{SessionID: "test"}
	ev := Event{Action: "tw.fire"}

	Route(w, "tw", sess, ev)

	if sess.Effects.Toast != "fired" {
		t.Errorf("toast = %q, want fired", sess.Effects.Toast)
	}
}

// toastWidget tests that Session side-effects work inside Handle.
type toastWidget struct{}

func (w toastWidget) Render() node.Node { return span.Text("toast") }
func (w toastWidget) Handle(sess Session, ev Event) Component {
	if ev.Action == "fire" {
		sess.Toast("fired")
	}
	return w
}

func TestWithAction(t *testing.T) {
	ev := Event{
		Action: "chat.send",
		Data:   map[string]string{"value": "hello"},
	}
	scoped := ev.WithAction("send")
	if scoped.Action != "send" {
		t.Errorf("Action = %q, want send", scoped.Action)
	}
	// Original unchanged.
	if ev.Action != "chat.send" {
		t.Errorf("original Action = %q, want chat.send", ev.Action)
	}
	// Data preserved.
	if scoped.Value() != "hello" {
		t.Errorf("Value = %q, want hello", scoped.Value())
	}
}

// equalWidget implements EqualComponent for testing.
type equalWidget struct {
	ID    int
	Items []string
}

func (w equalWidget) Render() node.Node { return span.Text("eq") }
func (w equalWidget) Handle(sess Session, ev Event) Component {
	return w
}
func (w equalWidget) EqualComponent(other Component) bool {
	o, ok := other.(equalWidget)
	if !ok {
		return false
	}
	return w.ID == o.ID
}

func TestEqualComponentInterface(t *testing.T) {
	a := equalWidget{ID: 1, Items: []string{"a"}}
	b := equalWidget{ID: 1, Items: []string{"b"}}
	c := equalWidget{ID: 2, Items: []string{"a"}}

	// Same ID → equal (ignores Items).
	if !a.EqualComponent(b) {
		t.Error("a and b should be equal (same ID)")
	}
	// Different ID → not equal.
	if a.EqualComponent(c) {
		t.Error("a and c should not be equal (different ID)")
	}
}
