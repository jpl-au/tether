package tether

import "testing"

func TestInvalidStatusTransitionPanics(t *testing.T) {
	tests := []struct {
		name string
		from Status
		to   Status
	}{
		{"Destroyed to Active", Destroyed, Active},
		{"Active to Pending", Active, Pending},
		{"Pending to Frozen", Pending, Frozen},
		{"Pending to Destroyed", Pending, Destroyed},
		{"Frozen to Frozen", Frozen, Frozen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &StatefulSession[counterState]{}
			sess.status.Store(int32(tt.from))

			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic for %s -> %s", tt.from, tt.to)
				}
			}()
			sess.transition(tt.to)
		})
	}
}

func TestValidStatusTransitions(t *testing.T) {
	tests := []struct {
		name string
		from Status
		to   Status
	}{
		{"Pending to Active", Pending, Active},
		{"Active to Frozen", Active, Frozen},
		{"Active to Destroyed", Active, Destroyed},
		{"Frozen to Active", Frozen, Active},
		{"Frozen to Destroyed", Frozen, Destroyed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &StatefulSession[counterState]{}
			sess.status.Store(int32(tt.from))

			// Should not panic.
			sess.transition(tt.to)

			got := Status(sess.status.Load())
			if got != tt.to {
				t.Errorf("status = %s, want %s", got, tt.to)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{Pending, "pending"},
		{Active, "active"},
		{Frozen, "frozen"},
		{Destroyed, "destroyed"},
		{0, "unknown"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("Status(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}
