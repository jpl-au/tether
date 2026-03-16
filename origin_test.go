package tether

import (
	"net/http"
	"testing"
)

// TestCrossOriginProtectionWithTrustedOrigins verifies that the stdlib
// CrossOriginProtection correctly allows trusted origins and blocks
// untrusted ones on state-changing methods.
func TestCrossOriginProtectionWithTrustedOrigins(t *testing.T) {
	csrf := http.NewCrossOriginProtection()
	csrf.AddTrustedOrigin("https://example.com")
	csrf.AddTrustedOrigin("https://staging.example.com")

	tests := []struct {
		name    string
		method  string
		origin  string
		allowed bool
	}{
		{"trusted origin POST", "POST", "https://example.com", true},
		{"trusted staging POST", "POST", "https://staging.example.com", true},
		{"untrusted origin POST", "POST", "https://evil.com", false},
		{"no origin POST", "POST", "", true},
		{"GET always allowed", "GET", "https://evil.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Method: tt.method, Header: http.Header{}, Host: "example.com"}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			err := csrf.Check(r)
			got := err == nil
			if got != tt.allowed {
				t.Errorf("Check() error = %v, want allowed = %v", err, tt.allowed)
			}
		})
	}
}

// TestCrossOriginProtectionSameHostFallback verifies that without
// trusted origins, the stdlib falls back to comparing Origin hostname
// against Host header.
func TestCrossOriginProtectionSameHostFallback(t *testing.T) {
	csrf := http.NewCrossOriginProtection()

	tests := []struct {
		name    string
		method  string
		origin  string
		host    string
		allowed bool
	}{
		{"same origin POST", "POST", "https://example.com", "example.com", true},
		{"cross origin POST", "POST", "https://evil.com", "example.com", false},
		{"no origin POST", "POST", "", "example.com", true},
		{"GET always allowed", "GET", "https://evil.com", "example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Method: tt.method, Header: http.Header{}, Host: tt.host}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			err := csrf.Check(r)
			got := err == nil
			if got != tt.allowed {
				t.Errorf("Check() error = %v, want allowed = %v", err, tt.allowed)
			}
		})
	}
}

// TestWSOriginAllowedWithTrustedOrigins verifies that the WebSocket-
// specific origin check blocks cross-origin upgrades even though they
// are GET requests. This prevents cross-site WebSocket hijacking.
func TestWSOriginAllowedWithTrustedOrigins(t *testing.T) {
	h := &Handler[counterState]{
		cfg: Config[counterState]{
			Security: Security{
				TrustedOrigins: []string{"https://example.com", "https://staging.example.com"},
			},
		},
	}

	tests := []struct {
		name    string
		origin  string
		allowed bool
	}{
		{"trusted origin", "https://example.com", true},
		{"trusted staging", "https://staging.example.com", true},
		{"cross origin blocked", "https://evil.com", false},
		{"no origin header", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Method: "GET", Header: http.Header{}, Host: "example.com"}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := h.wsOriginAllowed(r); got != tt.allowed {
				t.Errorf("wsOriginAllowed() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

// TestWSOriginAllowedSameHostFallback verifies that without trusted
// origins, the WebSocket check falls back to hostname comparison.
func TestWSOriginAllowedSameHostFallback(t *testing.T) {
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
		{"same host with port", "https://example.com", "example.com:8080", true},
		{"cross origin", "https://evil.com", "example.com", false},
		{"no origin", "", "example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Method: "GET", Header: http.Header{}, Host: tt.host}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := h.wsOriginAllowed(r); got != tt.allowed {
				t.Errorf("wsOriginAllowed() = %v, want %v", got, tt.allowed)
			}
		})
	}
}
