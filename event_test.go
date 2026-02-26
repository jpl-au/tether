package poly

import "testing"

func TestEventValue(t *testing.T) {
	ev := Event{Data: map[string]string{"value": "hello"}}
	if ev.Value() != "hello" {
		t.Errorf("Value() = %q, want %q", ev.Value(), "hello")
	}
}

func TestEventValueEmpty(t *testing.T) {
	ev := Event{}
	if ev.Value() != "" {
		t.Errorf("Value() = %q, want empty", ev.Value())
	}
}

func TestEventKey(t *testing.T) {
	ev := Event{Data: map[string]string{"key": "Enter"}}
	if got := ev.Key(); got != "Enter" {
		t.Errorf("Key() = %q, want %q", got, "Enter")
	}
}

func TestEventGet(t *testing.T) {
	ev := Event{Data: map[string]string{"name": "Alice"}}

	v, ok := ev.Get("name")
	if !ok || v != "Alice" {
		t.Errorf("Get(name) = (%q, %v), want (Alice, true)", v, ok)
	}

	_, ok = ev.Get("missing")
	if ok {
		t.Error("Get(missing) reported ok, want false")
	}
}

func TestEventInt(t *testing.T) {
	ev := Event{Data: map[string]string{"count": "42"}}

	n, err := ev.Int("count")
	if err != nil || n != 42 {
		t.Errorf("Int(count) = (%d, %v), want (42, nil)", n, err)
	}

	_, err = ev.Int("missing")
	if err == nil {
		t.Error("Int(missing) returned nil error, want error")
	}
}

func TestEventFloat64(t *testing.T) {
	ev := Event{Data: map[string]string{"price": "9.99"}}

	n, err := ev.Float64("price")
	if err != nil || n != 9.99 {
		t.Errorf("Float64(price) = (%f, %v), want (9.99, nil)", n, err)
	}
}

func TestEventBool(t *testing.T) {
	ev := Event{Data: map[string]string{"checked": "true", "other": "false"}}

	if !ev.Bool("checked") {
		t.Error("Bool(checked) = false, want true")
	}
	if ev.Bool("other") {
		t.Error("Bool(other) = true, want false")
	}
	if ev.Bool("missing") {
		t.Error("Bool(missing) = true, want false")
	}
}
