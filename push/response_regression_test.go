package push

import (
	"net/http"
	"strings"
	"testing"
)

type responseBody struct {
	*strings.Reader
	closed bool
}

func (b *responseBody) Close() error { b.closed = true; return nil }

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendDrainsAndClosesResponse(t *testing.T) {
	pub, priv, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{201, 410, 500} {
		body := &responseBody{Reader: strings.NewReader("push service response")}
		sender := NewSender(Config{VAPIDPublicKey: pub, VAPIDPrivateKey: priv, Subject: "mailto:test@example.com", HTTPClient: &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
		})}})
		err := sender.Send(newTestSubscription(t, "https://push.example.com/send"), Notification{Title: "test"})
		if (err == nil) != (status == 201) {
			t.Errorf("status %d: unexpected send error %v", status, err)
		}
		if !body.closed || body.Len() != 0 {
			t.Errorf("status %d: response retained (closed=%t remaining=%d)", status, body.closed, body.Len())
		}
	}
}
