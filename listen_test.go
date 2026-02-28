package poly

import (
	"os"
	"testing"
)

func TestResolveAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		port string // PORT env var; empty means unset
		want string
	}{
		{name: "explicit", addr: ":3000", want: ":3000"},
		{name: "explicit with host", addr: "0.0.0.0:9090", want: "0.0.0.0:9090"},
		{name: "PORT env var", port: "4000", want: ":4000"},
		{name: "default", want: ":8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.port != "" {
				t.Setenv("PORT", tt.port)
			} else {
				t.Setenv("PORT", "")
				os.Unsetenv("PORT")
			}
			if got := resolveAddr(tt.addr); got != tt.want {
				t.Errorf("resolveAddr(%q) with PORT=%q = %q, want %q",
					tt.addr, tt.port, got, tt.want)
			}
		})
	}
}

func TestDisplayURL(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{":8080", "http://localhost:8080"},
		{"0.0.0.0:8080", "http://localhost:8080"},
		{"[::]:8080", "http://localhost:8080"},
		{"127.0.0.1:3000", "http://127.0.0.1:3000"},
		{"192.168.1.5:8080", "http://192.168.1.5:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := displayURL(tt.addr); got != tt.want {
				t.Errorf("displayURL(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestDisplayTLSURL(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{":443", "https://localhost:443"},
		{"0.0.0.0:443", "https://localhost:443"},
		{"example.com:443", "https://example.com:443"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := displayTLSURL(tt.addr); got != tt.want {
				t.Errorf("displayTLSURL(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}
