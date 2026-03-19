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

// TestWSOriginAllowedSecFetchSite verifies that wsOriginAllowed uses
// Sec-Fetch-Site as the primary signal for modern browsers.
func TestWSOriginAllowedSecFetchSite(t *testing.T) {
	h := &Handler[counterState]{
		app: App{},
		cfg: LiveConfig[counterState]{},
	}

	tests := []struct {
		name         string
		secFetchSite string
		origin       string
		host         string
		allowed      bool
	}{
		{"same-origin", "same-origin", "https://example.com", "example.com", true},
		{"none (direct navigation)", "none", "", "example.com", true},
		{"cross-site blocked", "cross-site", "https://evil.com", "example.com", false},
		{"same-site blocked", "same-site", "https://sub.example.com", "example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Method: "GET", Header: http.Header{}, Host: tt.host}
			r.Header.Set("Sec-Fetch-Site", tt.secFetchSite)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := h.wsOriginAllowed(r); got != tt.allowed {
				t.Errorf("wsOriginAllowed() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

// TestWSOriginAllowedSecFetchSiteTrusted verifies that a cross-site
// request is allowed when the origin is in TrustedOrigins.
func TestWSOriginAllowedSecFetchSiteTrusted(t *testing.T) {
	h := &Handler[counterState]{
		app: App{Security: Security{
			TrustedOrigins: []string{"https://trusted.com"},
		}},
		cfg: LiveConfig[counterState]{},
	}

	r := &http.Request{Method: "GET", Header: http.Header{}, Host: "example.com"}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://trusted.com")

	if !h.wsOriginAllowed(r) {
		t.Error("trusted origin should be allowed even with cross-site Sec-Fetch-Site")
	}

	r2 := &http.Request{Method: "GET", Header: http.Header{}, Host: "example.com"}
	r2.Header.Set("Sec-Fetch-Site", "cross-site")
	r2.Header.Set("Origin", "https://evil.com")

	if h.wsOriginAllowed(r2) {
		t.Error("untrusted origin should be blocked with cross-site Sec-Fetch-Site")
	}
}

// TestWSOriginAllowedWithTrustedOrigins verifies that the WebSocket-
// specific origin check blocks cross-origin upgrades even though they
// are GET requests. This prevents cross-site WebSocket hijacking.
func TestWSOriginAllowedWithTrustedOrigins(t *testing.T) {
	h := &Handler[counterState]{
		app: App{Security: Security{
			TrustedOrigins: []string{"https://example.com", "https://staging.example.com"},
		}},
		cfg: LiveConfig[counterState]{},
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

// TestWSOriginAllowedHostFallback verifies that without trusted
// origins and without Sec-Fetch-Site, the WebSocket check compares
// Origin host:port against the Host header (matching the stdlib's
// fallback behaviour).
func TestWSOriginAllowedHostFallback(t *testing.T) {
	h := &Handler[counterState]{
		app: App{},
		cfg: LiveConfig[counterState]{},
	}

	tests := []struct {
		name    string
		origin  string
		host    string
		allowed bool
	}{
		{"exact match", "https://example.com", "example.com", true},
		{"port mismatch rejected", "https://example.com", "example.com:8080", false},
		{"origin with port matches host with port", "https://example.com:8080", "example.com:8080", true},
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
