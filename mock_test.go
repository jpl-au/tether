package poly

import (
	"io"
	"sync"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// patchUpdates returns all updates that contained patches (no morphs).
func patchUpdates(updates []Update) []Update {
	var result []Update
	for _, u := range updates {
		if len(u.Patches) > 0 && len(u.Morphs) == 0 {
			result = append(result, u)
		}
	}
	return result
}

// morphUpdates returns all updates that contained morphs.
func morphUpdates(updates []Update) []Update {
	var result []Update
	for _, u := range updates {
		if len(u.Morphs) > 0 {
			result = append(result, u)
		}
	}
	return result
}

// mockTransport records sent updates and replays queued events,
// allowing session event loop tests without a real connection.
type mockTransport struct {
	mu      sync.Mutex
	events  []Event
	updates []Update
	closed  bool
}

func (m *mockTransport) SendUpdate(update Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, update)
	return nil
}

func (m *mockTransport) ReceiveEvent() (Event, error) {
	m.mu.Lock()
	if len(m.events) == 0 {
		m.mu.Unlock()
		return Event{}, io.EOF
	}
	ev := m.events[0]
	m.events = m.events[1:]
	m.mu.Unlock()
	return ev, nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

type counterState struct {
	Count int
}

func renderCounter(state counterState) node.Node {
	return div.New(
		span.Textf("Count: %d", state.Count).Dynamic("count"),
	)
}

func handleCounter(_ *Session[counterState], state counterState, ev Event) HandleResult[counterState] {
	switch ev.Action {
	case "increment":
		state.Count++
	case "decrement":
		state.Count--
	}
	return Result(state)
}

// newTestSession creates a session with a seeded differ, ready for
// testing. Caller provides the transport with queued events.
func newTestSession(state counterState, mt *mockTransport) *Session[counterState] {
	differ := jit.NewDiffer()
	sess := &Session[counterState]{
		id:        "test",
		state:     state,
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
	}
	tree := sess.render(sess.state)
	differ.Render(tree)
	return sess
}
