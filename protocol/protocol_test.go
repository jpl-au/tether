package protocol

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		p    Protocol
		want string
	}{
		{Auto, "auto"},
		{HTTP1, "HTTP/1.1"},
		{HTTP2, "HTTP/2"},
		{HTTP3, "HTTP/3"},
		{0, "unknown"},
		{99, "unknown"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("Protocol(%d).String() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestConstants(t *testing.T) {
	// Zero value must not equal any named constant - this is how
	// Live() detects "not set" and falls back to Auto.
	if Auto == 0 {
		t.Fatal("Auto must not be zero")
	}
	// Constants must be distinct.
	seen := map[Protocol]string{}
	for _, pair := range []struct {
		p    Protocol
		name string
	}{
		{Auto, "Auto"},
		{HTTP1, "HTTP1"},
		{HTTP2, "HTTP2"},
		{HTTP3, "HTTP3"},
	} {
		if prev, ok := seen[pair.p]; ok {
			t.Fatalf("%s and %s have the same value %d", pair.name, prev, pair.p)
		}
		seen[pair.p] = pair.name
	}
}
