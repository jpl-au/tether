package ws

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/lxzan/gws"
)

func TestNormalCloseUsesStructuredCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		normal bool
	}{
		{"normal", &gws.CloseError{Code: 1000}, true},
		{"wrapped", fmt.Errorf("transport: %w", &gws.CloseError{Code: 1001}), true},
		{"reason", &gws.CloseError{Code: 1008, Reason: []byte("code=1000")}, false},
		{"unstructured", errors.New("unexpected code=1000"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNormalClose(tc.err); got != tc.normal {
				t.Errorf("normal=%t, want %t", got, tc.normal)
			}
		})
	}
}

func TestMessageAfterCloseIsDiscarded(t *testing.T) {
	tp, _ := dial(t)
	tp.closeWithErr(io.EOF)
	for range 64 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("message raced transport close: %v", r)
				}
			}()
			(&eventHandler{}).OnMessage(tp.conn, &gws.Message{Data: bytes.NewBufferString(`{"type":"click","action":"late"}`)})
		}()
	}
	if _, err := tp.ReceiveEvent(); !errors.Is(err, io.EOF) {
		t.Errorf("closed receive: %v", err)
	}
}
