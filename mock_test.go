package poly

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// patchMessages returns all decoded messages that contained patches (no morphs).
func patchMessages(sent [][]byte) []updateMessage {
	var result []updateMessage
	for _, data := range sent {
		var msg updateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if len(msg.Patches) > 0 && len(msg.Morphs) == 0 {
			result = append(result, msg)
		}
	}
	return result
}

// morphMessages returns all decoded messages that contained morphs.
func morphMessages(sent [][]byte) []updateMessage {
	var result []updateMessage
	for _, data := range sent {
		var msg updateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if len(msg.Morphs) > 0 {
			result = append(result, msg)
		}
	}
	return result
}

// decodeMessage unmarshals a single raw JSON message.
func decodeMessage(data []byte) updateMessage {
	var msg updateMessage
	json.Unmarshal(data, &msg)
	return msg
}

// mockTransport records sent bytes and replays queued events,
// allowing session event loop tests without a real connection.
type mockTransport struct {
	mu     sync.Mutex
	events []Event
	sent   [][]byte
	closed bool
}

func (m *mockTransport) Send(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.sent = append(m.sent, cp)
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

func handleCounter(_ *Session[counterState], state counterState, ev Event) counterState {
	switch ev.Action {
	case "increment":
		state.Count++
	case "decrement":
		state.Count--
	}
	return state
}

// newTestSession creates a session with a seeded differ, ready for
// testing. The session has channels and a logger. Caller must start
// the transport reader and run loop:
//
//	go sess.readTransport(sess.events)
//	go sess.run()
func newTestSession(state counterState, mt *mockTransport) *Session[counterState] {
	differ := jit.NewDiffer()
	ctx, cancel := context.WithCancel(context.Background())
	sess := &Session[counterState]{
		id:        "test",
		state:     state,
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		transport: mt,
		logger:    slog.Default().WithGroup("session").With("id", "test"),
		events:    make(chan Event),
		cmds:      make(chan func(), defaultCmdBufferSize),
		loopDone:  make(chan struct{}),
		ctx:       ctx,
		stop:      cancel,
	}
	tree := sess.render(sess.state)
	differ.Render(tree)
	return sess
}
