package wire

import "testing"

func TestFormatString(t *testing.T) {
	tests := []struct {
		format Format
		want   string
	}{
		{JSON, "json"},
		{CBOR, "cbor"},
		{Format(99), "json"}, // unknown defaults to json
	}
	for _, tt := range tests {
		if got := tt.format.String(); got != tt.want {
			t.Errorf("Format(%d).String() = %q, want %q", tt.format, got, tt.want)
		}
	}
}
