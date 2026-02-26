package poly

import "testing"

func TestBindBasicForm(t *testing.T) {
	ev := Event{Data: map[string]string{
		"email": "alice@example.com",
		"age":   "30",
	}}

	var form struct {
		Email string `poly:"email"`
		Age   int    `poly:"age"`
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
		Name string // no tag — uses lowercased field name
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
		Email string `poly:"email"`
		Age   int    `poly:"age"`
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
		Agree bool `poly:"agree"`
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
		Amount float64 `poly:"amount"`
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
		Count int `poly:"count"`
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
		ID int64 `poly:"id"`
	}
	if err := ev.Bind(&form); err != nil {
		t.Fatalf("Bind() error: %v", err)
	}
	if form.ID != 9223372036854775807 {
		t.Errorf("ID = %d, want max int64", form.ID)
	}
}
