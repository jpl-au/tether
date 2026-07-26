package bind

import (
	"os"
	"strings"
	"testing"
)

// TestEventAttrPrefixMatchesClient guards the one string the Go and JS
// halves must agree on. Every server event binding renders
// data-tether-event-<name>, and the client's discovery scan finds them
// by that prefix alone - so a change on one side and not the other
// makes every binding on the page silently stop firing.
func TestEventAttrPrefixMatchesClient(t *testing.T) {
	src, err := os.ReadFile("../client/tether.js")
	if err != nil {
		t.Fatalf("read client runtime: %v", err)
	}
	want := `var eventAttrPrefix = "data-` + eventAttr + `";`
	if !strings.Contains(string(src), want) {
		t.Errorf("client/tether.js does not declare %s\nbind.eventAttr is %q, so the client must scan for that prefix", want, eventAttr)
	}
}

// TestOnAcceptsAnyDomEvent is the regression test for the defect this
// mechanism replaced: a fixed list in the client meant bind.Event
// rendered attributes nothing listened for, so the binding did nothing
// at all. Discovery removes the list, so no event name is unreachable.
func TestOnAcceptsAnyDomEvent(t *testing.T) {
	for _, name := range []string{
		"click", "dblclick", "wheel", "mouseenter", "mouseleave",
		"scroll", "resize", "pointerdown", "animationend",
		"sl-change", "cart:updated", "my_event", "toggle",
	} {
		opt := On(name, "act")
		if want := eventAttr + name; opt.key != want {
			t.Errorf("On(%q) key = %q, want %q", name, opt.key, want)
		}
	}
}

// TestOnRejectsUnbindableName pins the one case that cannot work: a
// name HTML cannot carry in an attribute name. Panicking matches the
// rest of the package (Debounce, MinLength, OnClientEvent) and is the
// only option that is not silent in production, where dev.Warn is a
// no-op.
func TestOnRejectsUnbindableName(t *testing.T) {
	for _, name := range []string{"", "mouseOver", "my event", "a\"b", "a>b", "a=b"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("On(%q) should panic - the name cannot be carried in an attribute name", name)
				}
			}()
			On(name, "act")
		}()
	}
}
