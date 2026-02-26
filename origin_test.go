package poly

import (
	"net/http"
	"testing"
)

func TestOriginAllowedWithExplicitList(t *testing.T) {
	h := &Handler[counterState]{
		cfg: Config[counterState]{
			Security: Security{
				AllowedOrigins: []string{"https://example.com", "https://staging.example.com"},
			},
		},
	}

	tests := []struct {
		name    string
		origin  string
		host    string
		allowed bool
	}{
		{"matching origin", "https://example.com", "example.com", true},
		{"matching staging", "https://staging.example.com", "example.com", true},
		{"wrong origin", "https://evil.com", "example.com", false},
		{"no origin header", "", "example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}, Host: tt.host}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := h.originAllowed(r); got != tt.allowed {
				t.Errorf("originAllowed() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestOriginAllowedFallsBackToHostMatch(t *testing.T) {
	h := &Handler[counterState]{
		cfg: Config[counterState]{},
	}

	tests := []struct {
		name    string
		origin  string
		host    string
		allowed bool
	}{
		{"same host", "https://example.com", "example.com", true},
		{"different host", "https://evil.com", "example.com", false},
		{"no origin header", "", "example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}, Host: tt.host}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := h.originAllowed(r); got != tt.allowed {
				t.Errorf("originAllowed() = %v, want %v", got, tt.allowed)
			}
		})
	}
}
