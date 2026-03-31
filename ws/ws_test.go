package ws

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jpl-au/tether/event"
	xport "github.com/jpl-au/tether/internal/transport"
	"github.com/lxzan/gws"
)

// testClient wraps a gws client connection with a channel-based
// read interface so tests can synchronously receive messages.
type testClient struct {
	conn     *gws.Conn
	messages chan []byte
	closed   chan struct{}
	closeErr error
	once     sync.Once
}

// testClientHandler implements gws.Event for the test client,
// forwarding received messages to the testClient's channel.
type testClientHandler struct {
	gws.BuiltinEventHandler
	client *testClient
}

func (h *testClientHandler) OnMessage(_ *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	data := make([]byte, len(msg.Bytes()))
	copy(data, msg.Bytes())
	select {
	case h.client.messages <- data:
	case <-h.client.closed:
	}
}

func (h *testClientHandler) OnClose(_ *gws.Conn, err error) {
	h.client.once.Do(func() {
		h.client.closeErr = err
		close(h.client.closed)
	})
}

func (tc *testClient) readMessage() ([]byte, error) {
	select {
	case data, ok := <-tc.messages:
		if !ok {
			return nil, io.EOF
		}
		return data, nil
	case <-tc.closed:
		if tc.closeErr != nil {
			return nil, tc.closeErr
		}
		return nil, io.EOF
	}
}

func (tc *testClient) writeMessage(data []byte) error {
	return tc.conn.WriteMessage(gws.OpcodeText, data)
}

func (tc *testClient) writeClose(code uint16, reason []byte) error {
	return tc.conn.WriteClose(code, reason)
}

func (tc *testClient) close() error {
	tc.once.Do(func() {
		close(tc.closed)
	})
	return tc.conn.WriteClose(1000, nil)
}

// dial starts an httptest server that upgrades to WebSocket via ws.Upgrade,
// sends the server-side transport to the caller, and returns a gws
// client connection for the test to read/write against.
func dial(t *testing.T) (*transport, *testClient) {
	t.Helper()

	ready := make(chan *transport, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgradeFn := Upgrade()
		tp, err := upgradeFn(w, r)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		ready <- tp.(*transport)
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	tc := &testClient{
		messages: make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
	handler := &testClientHandler{client: tc}
	conn, _, err := gws.NewClient(handler, &gws.ClientOption{Addr: wsURL})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	tc.conn = conn
	go conn.ReadLoop()
	t.Cleanup(func() { tc.close() })

	tp := <-ready
	return tp, tc
}

func TestSendDeliversJSON(t *testing.T) {
	tp, client := dial(t)

	data := []byte(`{"type":"update","patches":[{"key":"count","html":"<span>1</span>"}],"title":"hello"}`)
	if err := tp.Send(data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	received, err := client.readMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}

	if string(received) != string(data) {
		t.Errorf("received %q, want %q", received, data)
	}

	var decoded map[string]any
	if err := json.Unmarshal(received, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["type"] != "update" {
		t.Errorf("expected type \"update\", got %v", decoded["type"])
	}
	if decoded["title"] != "hello" {
		t.Errorf("expected title \"hello\", got %v", decoded["title"])
	}
}

func TestSendPreservesAngleBrackets(t *testing.T) {
	tp, client := dial(t)

	// Angle brackets should pass through unchanged - the session
	// encodes with SetEscapeHTML(false) before calling Send.
	data := []byte(`{"type":"update","patches":[{"key":"x","html":"<b>hi</b>"}]}`)
	if err := tp.Send(data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	received, err := client.readMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}

	raw := string(received)
	if strings.Contains(raw, `\u003c`) || strings.Contains(raw, `\u003e`) {
		t.Errorf("HTML was escaped: %s", raw)
	}
}

func TestReceiveEventReadsClientJSON(t *testing.T) {
	tp, client := dial(t)

	want := xport.Event{Type: event.Click, Action: "increment"}
	data, _ := json.Marshal(want)
	if err := client.writeMessage(data); err != nil {
		t.Fatalf("client write: %v", err)
	}

	got, err := tp.ReceiveEvent()
	if err != nil {
		t.Fatalf("ReceiveEvent: %v", err)
	}
	if got.Type != want.Type || got.Action != want.Action {
		t.Errorf("expected {%s %s}, got {%s %s}", want.Type, want.Action, got.Type, got.Action)
	}
}

func TestReceiveEventReturnsEOFOnNormalClose(t *testing.T) {
	tp, client := dial(t)

	go func() {
		client.writeClose(1000, []byte("bye"))
	}()

	_, err := tp.ReceiveEvent()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestReceiveEventReturnsEOFOnGoingAway(t *testing.T) {
	tp, client := dial(t)

	go func() {
		client.writeClose(1001, []byte("navigating"))
	}()

	_, err := tp.ReceiveEvent()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestCloseEndsConnection(t *testing.T) {
	tp, client := dial(t)

	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := client.readMessage()
	if err == nil {
		t.Fatal("expected error after server close, got nil")
	}
}

// dialWith creates a server/client pair where both sides use the
// given Options and client-side PermessageDeflate configuration.
func dialWith(t *testing.T, opts Options, clientDeflate gws.PermessageDeflate) (*transport, *testClient) {
	t.Helper()

	ready := make(chan *transport, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgradeFn := Upgrade(opts)
		tp, err := upgradeFn(w, r)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		ready <- tp.(*transport)
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	tc := &testClient{
		messages: make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
	handler := &testClientHandler{client: tc}
	conn, _, err := gws.NewClient(handler, &gws.ClientOption{
		Addr:              wsURL,
		PermessageDeflate: clientDeflate,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	tc.conn = conn
	go conn.ReadLoop()
	t.Cleanup(func() { tc.close() })

	tp := <-ready
	return tp, tc
}

func TestCompressedRoundTrip(t *testing.T) {
	tp, client := dialWith(t, Options{}, gws.PermessageDeflate{Enabled: true})

	data := []byte(`{"type":"update","patches":[{"key":"content","html":"<div class=\"container\"><p>Hello, world!</p></div>"}]}`)
	if err := tp.Send(data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	received, err := client.readMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(received) != string(data) {
		t.Errorf("received %q, want %q", received, data)
	}
}

func TestCompressionDisabled(t *testing.T) {
	opts := Options{Compression: Compression{Disabled: true}}
	tp, client := dialWith(t, opts, gws.PermessageDeflate{Enabled: true})

	data := []byte(`{"type":"update","patches":[{"key":"x","html":"<span>no compression</span>"}]}`)
	if err := tp.Send(data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	received, err := client.readMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(received) != string(data) {
		t.Errorf("received %q, want %q", received, data)
	}
}

func TestContextTakeoverRoundTrip(t *testing.T) {
	opts := Options{Compression: Compression{ContextTakeover: true}}
	clientDeflate := gws.PermessageDeflate{
		Enabled:               true,
		ServerContextTakeover: true,
		ClientContextTakeover: true,
	}
	tp, client := dialWith(t, opts, clientDeflate)

	// Send several messages to exercise the sliding window across
	// messages - context takeover means the compressor retains state.
	for i := range 3 {
		data := []byte(`{"type":"update","patches":[{"key":"count","html":"<span>` + strings.Repeat("x", 100) + `</span>"}]}`)
		if err := tp.Send(data); err != nil {
			t.Fatalf("Send[%d]: %v", i, err)
		}
		received, err := client.readMessage()
		if err != nil {
			t.Fatalf("client read[%d]: %v", i, err)
		}
		if string(received) != string(data) {
			t.Errorf("message %d: received %q, want %q", i, received, data)
		}
	}
}
