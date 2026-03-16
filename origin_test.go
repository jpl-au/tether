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
