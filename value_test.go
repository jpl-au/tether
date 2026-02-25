package poly

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestValueGetSet(t *testing.T) {
	v := NewValue(10)

	if got := v.Get(); got != 10 {
		t.Errorf("Get() = %d, want 10", got)
	}

	v.Set(42)

	if got := v.Get(); got != 42 {
		t.Errorf("Get() after Set = %d, want 42", got)
	}
}

func TestValueUpdate(t *testing.T) {
	v := NewValue(5)

	v.Update(func(n int) int { return n + 3 })

	if got := v.Get(); got != 8 {
		t.Errorf("Get() after Update = %d, want 8", got)
	}
}

func TestValuePublishesOnSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := NewValue(0)

		var received int
		v.bus.Subscribe(context.Background(), func(val int) {
			received = val
		})

		v.Set(99)

		if received != 99 {
			t.Errorf("subscriber received %d, want 99", received)
		}
	})
}

func TestValuePublishesOnUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := NewValue(10)

		var received int
		v.bus.Subscribe(context.Background(), func(val int) {
			received = val
		})

		v.Update(func(n int) int { return n * 2 })

		if received != 20 {
			t.Errorf("subscriber received %d, want 20", received)
		}
	})
}

func TestValueLen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := NewValue("")

		if v.Len() != 0 {
			t.Fatalf("empty value Len() = %d", v.Len())
		}

		ctx, cancel := context.WithCancel(context.Background())
		v.bus.Subscribe(ctx, func(string) {})

		if v.Len() != 1 {
			t.Fatalf("Len() = %d, want 1", v.Len())
		}

		cancel()
		synctest.Wait()

		if v.Len() != 0 {
			t.Fatalf("Len() after cancel = %d, want 0", v.Len())
		}
	})
}
