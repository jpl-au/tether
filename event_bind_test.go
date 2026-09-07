package tether

import (
	"strings"
	"testing"
	"time"
)

func TestBindBasicForm(t *testing.T) {
	ev := Event{Data: map[string]string{
		"email": "alice@example.com",
		"age":   "30",
	}}

	var form struct {
		Email string `tether:"email"`
		Age   int    `tether:"age"`
	}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind() error: %v", err)
	}
	if form.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", form.Email, "alice@example.com")
	}
	if form.Age != 30 {
		t.Errorf("Age = %d, want 30", form.Age)
	}
}

func TestBindDefaultFieldName(t *testing.T) {
	ev := Event{Data: map[string]string{"name": "Bob"}}

	var form struct {
		Name string // no tag - uses lowercased field name
	}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind() error: %v", err)
	}
	if form.Name != "Bob" {
		t.Errorf("Name = %q, want %q", form.Name, "Bob")
	}
}

func TestBindMissingFieldsAreSkipped(t *testing.T) {
	ev := Event{Data: map[string]string{"email": "alice@example.com"}}

	var form struct {
		Email string `tether:"email"`
		Age   int    `tether:"age"`
	}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind() error: %v", err)
	}
	if form.Age != 0 {
		t.Errorf("Age = %d, want 0 (zero value for missing field)", form.Age)
	}
}

func TestBindBoolField(t *testing.T) {
	ev := Event{Data: map[string]string{"agree": "true"}}

	var form struct {
		Agree bool `tether:"agree"`
	}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind() error: %v", err)
	}
	if !form.Agree {
		t.Error("Agree = false, want true")
	}
}

func TestBindFloat64Field(t *testing.T) {
	ev := Event{Data: map[string]string{"amount": "19.95"}}

	var form struct {
		Amount float64 `tether:"amount"`
	}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind() error: %v", err)
	}
	if form.Amount != 19.95 {
		t.Errorf("Amount = %f, want 19.95", form.Amount)
	}
}

func TestBindInvalidInt(t *testing.T) {
	ev := Event{Data: map[string]string{"count": "abc"}}

	var form struct {
		Count int `tether:"count"`
	}
	if err := ev.Bind(&form); err == nil {
		t.Error("expected error for non-integer value")
	}
}

func TestBindNonPointerReturnsError(t *testing.T) {
	ev := Event{}
	var form struct{}
	if err := ev.Bind(form); err == nil {
		t.Error("expected error for non-pointer argument")
	}
}

func TestBindNilPointerReturnsError(t *testing.T) {
	ev := Event{}
	if err := ev.Bind((*struct{})(nil)); err == nil {
		t.Error("expected error for nil pointer")
	}
}

func TestBindInt64Field(t *testing.T) {
	ev := Event{Data: map[string]string{"id": "9223372036854775807"}}

	var form struct {
		ID int64 `tether:"id"`
	}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind() error: %v", err)
	}
	if form.ID != 9223372036854775807 {
		t.Errorf("ID = %d, want max int64", form.ID)
	}
}

func checkBindInteger[T comparable](t *testing.T, raw string, want T, invalid ...string) {
	t.Helper()
	form := struct {
		Value T `tether:"amount"`
	}{}
	ev := Event{Data: map[string]string{"amount": raw}}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind(%q): %v", raw, err)
	}
	if form.Value != want {
		t.Fatalf("Bind(%q) = %v, want %v", raw, form.Value, want)
	}
	for _, raw := range invalid {
		ev.Data["amount"] = raw
		if err := ev.Bind(&form); err == nil || !strings.Contains(err.Error(), `"amount"`) {
			t.Errorf("Bind(%q) error = %v, want an error identifying amount", raw, err)
		}
		if form.Value != want {
			t.Errorf("Bind(%q) changed field to %v on error", raw, form.Value)
		}
	}
}

func TestBindIntegerWidths(t *testing.T) {
	t.Run("int8", func(t *testing.T) {
		checkBindInteger(t, "-128", int8(-128), "-129", "128")
	})
	t.Run("int16", func(t *testing.T) {
		checkBindInteger(t, "32767", int16(32767), "-32769", "32768")
	})
	t.Run("int32", func(t *testing.T) {
		checkBindInteger(t, "2147483647", int32(2147483647), "-2147483649", "2147483648")
	})
	t.Run("uint", func(t *testing.T) {
		checkBindInteger(t, "42", uint(42), "-1", "18446744073709551616")
	})
	t.Run("uint8", func(t *testing.T) {
		checkBindInteger(t, "255", uint8(255), "-1", "256")
	})
	t.Run("uint16", func(t *testing.T) {
		checkBindInteger(t, "65535", uint16(65535), "-1", "65536")
	})
	t.Run("uint32", func(t *testing.T) {
		checkBindInteger(t, "4294967295", uint32(4294967295), "-1", "4294967296")
	})
	t.Run("uint64", func(t *testing.T) {
		checkBindInteger(t, "18446744073709551615", uint64(18446744073709551615), "-1", "18446744073709551616")
	})
	t.Run("named integer", func(t *testing.T) {
		type quantity uint32
		checkBindInteger(t, "100", quantity(100), "-1", "4294967296")
	})
	t.Run("int64 remains numeric", func(t *testing.T) {
		checkBindInteger(t, "5", int64(5), "5s")
	})
}

func TestBindDuration(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want time.Duration
	}{
		{"5s", 5 * time.Second},
		{"250ms", 250 * time.Millisecond},
		{"1h30m", 90 * time.Minute},
		{"-1.5s", -1500 * time.Millisecond},
		{"0", 0},
		{"5000000000", 5 * time.Second},
		{"-5000000000", -5 * time.Second},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			checkBindInteger(t, tt.raw, tt.want, "later", "5seconds", "9223372036854775808", "9999999999999999999h")
		})
	}
}
