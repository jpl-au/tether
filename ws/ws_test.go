package ws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	tether "github.com/jpl-au/fluent-tether"
	"github.com/jpl-au/fluent-tether/event"
)

// dial starts an httptest server that upgrades to WebSocket via ws.Upgrade,
// sends the server-side transport to the caller, and returns a client
// connection for the test to read/write against. The handler stays
// alive until the test completes, mirroring production where the
// session event loop holds the connection open.
func dial(t *testing.T) (*transport, *websocket.Conn) {
	t.Helper()

	ready := make(chan *transport, 1)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgradeFn := Upgrade()
		tp, err := upgradeFn(w, r)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		ready <- tp.(*transport)
		// Block until the test finishes so r.Context() stays alive.
		<-done
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	client, _, err := websocket.Dial(ctx, srv.URL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	t.Cleanup(func() { client.CloseNow() })

	tp := <-ready
	return tp, client
}

func TestSendDeliversJSON(t *testing.T) {
	tp, client := dial(t)

	data := []byte(`{"type":"update","patches":[{"key":"count","html":"<span>1</span>"}],"title":"hello"}`)
	if err := tp.Send(data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, received, err := client.Read(context.Background())
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

	// Angle brackets should pass through unchanged — the session
	// encodes with SetEscapeHTML(false) before calling Send.
	data := []byte(`{"type":"update","patches":[{"key":"x","html":"<b>hi</b>"}]}`)
	if err := tp.Send(data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, received, err := client.Read(context.Background())
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

	want := tether.Event{Type: event.Click, Action: "increment"}
	data, _ := json.Marshal(want)
	if err := client.Write(context.Background(), websocket.MessageText, data); err != nil {
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

	// Close in a goroutine — the close handshake needs the server
	// to echo the frame, which happens inside ReceiveEvent's Read.
	go client.Close(websocket.StatusNormalClosure, "bye")

	_, err := tp.ReceiveEvent()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestReceiveEventReturnsEOFOnGoingAway(t *testing.T) {
	tp, client := dial(t)

	go client.Close(websocket.StatusGoingAway, "navigating")

	_, err := tp.ReceiveEvent()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestCloseEndsConnection(t *testing.T) {
	tp, client := dial(t)

	// Start a client read so it can respond to the server's close frame.
	readErr := make(chan error, 1)
	go func() {
		_, _, err := client.Read(context.Background())
		readErr <- err
	}()

	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := <-readErr
	if err == nil {
		t.Fatal("expected error after server close, got nil")
	}
	status := websocket.CloseStatus(err)
	if status != websocket.StatusNormalClosure {
		t.Errorf("expected StatusNormalClosure, got %v (err: %v)", status, err)
	}
}
