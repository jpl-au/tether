package tether

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jpl-au/tether/protocol"
)

func TestDetectProtocol(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want protocol.Protocol
	}{
		{
			name: "plain HTTP is HTTP/1.1",
			req:  &http.Request{ProtoMajor: 1},
			want: protocol.HTTP1,
		},
		{
			name: "TLS with ProtoMajor 1 is HTTP/1.1",
			req:  &http.Request{ProtoMajor: 1, TLS: &tls.ConnectionState{}},
			want: protocol.HTTP1,
		},
		{
			name: "TLS with ProtoMajor 2 is HTTP/2",
			req:  &http.Request{ProtoMajor: 2, TLS: &tls.ConnectionState{}},
			want: protocol.HTTP2,
		},
		{
			name: "TLS with ProtoMajor 3 is HTTP/2",
			req:  &http.Request{ProtoMajor: 3, TLS: &tls.ConnectionState{}},
			want: protocol.HTTP2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectProtocol(tt.req)
			if got != tt.want {
				t.Errorf("detectProtocol() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveProtocol_Auto(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	req := &http.Request{ProtoMajor: 2, TLS: &tls.ConnectionState{}}
	got := resolveProtocol(protocol.Auto, req, logger)

	if got != protocol.HTTP2 {
		t.Errorf("resolveProtocol(Auto) = %v, want HTTP2", got)
	}
	if buf.Len() != 0 {
		t.Errorf("Auto mode should not log warnings, got: %s", buf.String())
	}
}

func TestResolveProtocol_ExplicitMatch(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	req := &http.Request{ProtoMajor: 2, TLS: &tls.ConnectionState{}}
	got := resolveProtocol(protocol.HTTP2, req, logger)

	if got != protocol.HTTP2 {
		t.Errorf("resolveProtocol(HTTP2) = %v, want HTTP2", got)
	}
	if buf.Len() != 0 {
		t.Errorf("matching protocol should not log warnings, got: %s", buf.String())
	}
}

func TestResolveProtocol_Mismatch(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Configure HTTP2 but wire is HTTP/1.1 (no TLS).
	req := &http.Request{ProtoMajor: 1}
	got := resolveProtocol(protocol.HTTP2, req, logger)

	if got != protocol.HTTP2 {
		t.Errorf("resolveProtocol should return configured value, got %v", got)
	}
	if !strings.Contains(buf.String(), "protocol mismatch") {
		t.Errorf("expected mismatch warning, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "HTTP/2") || !strings.Contains(buf.String(), "HTTP/1.1") {
		t.Errorf("warning should include both protocols, got: %s", buf.String())
	}
}

func TestResolveProtocol_MismatchReverse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Configure HTTP1 but wire is HTTP/2.
	req := &http.Request{ProtoMajor: 2, TLS: &tls.ConnectionState{}}
	got := resolveProtocol(protocol.HTTP1, req, logger)

	if got != protocol.HTTP1 {
		t.Errorf("resolveProtocol should return configured value, got %v", got)
	}
	if !strings.Contains(buf.String(), "protocol mismatch") {
		t.Errorf("expected mismatch warning, got: %s", buf.String())
	}
}
