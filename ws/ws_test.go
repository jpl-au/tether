package ws

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	tether "github.com/jpl-au/fluent-tether"
	"github.com/jpl-au/fluent-tether/event"
)

// dial starts an httptest server that upgrades to WebSocket via ws.Upgrade,
// sends the server-side transport to the caller, and returns a gorilla
// client connection for the test to read/write against.
func dial(t *testing.T) (*transport, *gorilla.Conn) {
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
		// gws hijacks the connection — the handler can return.
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := gorilla.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	tp := <-ready
	return tp, client
}

func TestSendDeliversJSON(t *testing.T) {
	tp, client := dial(t)

	data := []byte(`{"type":"update","patches":[{"key":"count","html":"<span>1</span>"}],"title":"hello"}`)
	if err := tp.Send(data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, received, err := client.ReadMessage()
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

	_, received, err := client.ReadMessage()
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
	if err := client.WriteMessage(gorilla.TextMessage, data); err != nil {
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
		msg := gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, "bye")
		client.WriteControl(gorilla.CloseMessage, msg, time.Now().Add(time.Second))
	}()

	_, err := tp.ReceiveEvent()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestReceiveEventReturnsEOFOnGoingAway(t *testing.T) {
	tp, client := dial(t)

	go func() {
		msg := gorilla.FormatCloseMessage(gorilla.CloseGoingAway, "navigating")
		client.WriteControl(gorilla.CloseMessage, msg, time.Now().Add(time.Second))
	}()

	_, err := tp.ReceiveEvent()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestCloseEndsConnection(t *testing.T) {
	tp, client := dial(t)

	readErr := make(chan error, 1)
	go func() {
		_, _, err := client.ReadMessage()
		readErr <- err
	}()

	if err := tp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := <-readErr
	if err == nil {
		t.Fatal("expected error after server close, got nil")
	}
	closeErr, ok := err.(*gorilla.CloseError)
	if !ok || closeErr.Code != gorilla.CloseNormalClosure {
		t.Errorf("expected CloseNormalClosure, got %v", err)
	}
}
