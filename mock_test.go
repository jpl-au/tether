package tether

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/wire"
)

// testMessage mirrors the JSON wire format for test assertions. Tests
// unmarshal transport bytes into this to inspect what the session sent.
type testMessage struct {
	Type     string            `json:"type"`
	Patches  []testPatch       `json:"patches,omitempty"`
	Morphs   []testMorph       `json:"morphs,omitempty"`
	URL      string            `json:"url,omitempty"`
	Replace  bool              `json:"replace,omitempty"`
	Title    string            `json:"title,omitempty"`
	Flash    map[string]string `json:"flash,omitempty"`
	Signals  map[string]any    `json:"signals,omitempty"`
	Announce string            `json:"announce,omitempty"`
	Toast    string            `json:"toast,omitempty"`
	EventID  string            `json:"event_id,omitempty"`
}

type testPatch struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

type testMorph struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

// patchMessages returns all decoded messages that contained patches (no morphs).
func patchMessages(sent [][]byte) []testMessage {
	var result []testMessage
	for _, data := range sent {
		var msg testMessage
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
func morphMessages(sent [][]byte) []testMessage {
	var result []testMessage
	for _, data := range sent {
		var msg testMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if len(msg.Morphs) > 0 {
			result = append(result, msg)
		}
	}
	return result
}

// decodeMessage unmarshals a single raw JSON message. Panics on
// malformed JSON so test failures surface immediately.
func decodeMessage(data []byte) testMessage {
	var msg testMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		panic("decodeMessage: " + err.Error())
	}
	return msg
}

// mockTransport delivers events from a slice then returns io.EOF.
// Use this for tests where the session should process events and
// then disconnect.
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

// connectedTransport stays connected until Close is called.
// ReceiveEvent blocks on a channel, so readTransport idles instead
// of returning EOF. Use this for tests that only exercise
// server-to-client behaviour (Broadcast, Signal, Observe, etc.).
type connectedTransport struct {
	mu     sync.Mutex
	ch     chan Event
	sent   [][]byte
	closed bool
}

func newConnectedTransport() *connectedTransport {
	return &connectedTransport{ch: make(chan Event)}
}

func (c *connectedTransport) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	c.sent = append(c.sent, cp)
	return nil
}

func (c *connectedTransport) ReceiveEvent() (Event, error) {
	ev, ok := <-c.ch
	if !ok {
		return Event{}, io.EOF
	}
	return ev, nil
}

func (c *connectedTransport) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.ch)
	}
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

func handleCounter(_ Session, state counterState, ev Event) counterState {
	switch ev.Action {
	case "increment":
		state.Count++
	case "decrement":
		state.Count--
	}
	return state
}

// newTestSession creates a session with a seeded differ, ready for
// testing. The session has channels configured. Caller must start
// the transport reader and run loop:
//
//	go sess.readTransport(sess.events)
//	go sess.run()
func newTestSession(state counterState, mt Transport) *LiveSession[counterState] {
	differ := jit.NewDiffer()
	ctx, cancel := context.WithCancel(context.Background())
	sess := &LiveSession[counterState]{
		id:        "test",
		state:     state,
		render:    renderCounter,
		handle:    handleCounter,
		differ:    differ,
		encoder:   wire.JSONEncoder{},
		transport: mt,
		events:    make(chan Event),
		cmds:      make(chan func(), defaultCmdBufferSize),
		fxCh:      make(chan func(*Effects), defaultCmdBufferSize),
		loopDone:  make(chan struct{}),
		ctx:       ctx,
		stop:      cancel,
	}
	tree := sess.render(sess.state)
	differ.Render(tree)
	return sess
}
