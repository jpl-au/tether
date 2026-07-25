package bind

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// eventTypeEntry matches one ["click", "tether-click"] pair from the
// eventTypes table in client/tether.js.
var eventTypeEntry = regexp.MustCompile(`\["([a-z]+)",\s*"tether-([a-z]+)"\]`)

// TestServerEventsMatchClient guards the seam between serverEvents and
// the eventTypes table in client/tether.js. An event Go accepts but the
// client never delegates renders an attribute nothing listens for - the
// binding then does nothing at all, with no error to point at. This
// test makes that mismatch a build failure rather than a silent no-op.
func TestServerEventsMatchClient(t *testing.T) {
	src, err := os.ReadFile("../client/tether.js")
	if err != nil {
		t.Fatalf("read client runtime: %v", err)
	}

	table := regexp.MustCompile(`(?s)var eventTypes = \[(.*?)\];`).FindSubmatch(src)
	if table == nil {
		t.Fatal("eventTypes table not found in client/tether.js - has it been renamed?")
	}

	var client []string
	for _, m := range eventTypeEntry.FindAllSubmatch(table[1], -1) {
		domEvent, attr := string(m[1]), string(m[2])
		if domEvent != attr {
			t.Errorf("client binds DOM event %q to data-tether-%s; bind.Event derives the attribute from the event name, so they must match", domEvent, attr)
		}
		client = append(client, domEvent)
	}
	if len(client) == 0 {
		t.Fatal("eventTypes table parsed as empty")
	}

	for _, e := range serverEvents {
		if !slices.Contains(client, e) {
			t.Errorf("bind.Event accepts %q but client/tether.js never delegates it - the binding would silently do nothing", e)
		}
	}
	for _, e := range client {
		if !slices.Contains(serverEvents, e) {
			t.Errorf("client/tether.js delegates %q but bind.Event rejects it - add it to serverEvents", e)
		}
	}
}

// TestEventRejectsUndelegatedType pins the construction-time panic that
// replaced the silent no-op.
func TestEventRejectsUndelegatedType(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected Event to panic for an event the client does not delegate")
		}
	}()
	Event("wheel", "scroll")
}
