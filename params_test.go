package tether

import (
	"net/url"
	"testing"
)

func TestParamsGet(t *testing.T) {
	p := Params{Query: url.Values{"q": {"hello"}}}
	if got := p.Get("q"); got != "hello" {
		t.Errorf("Get(q) = %q, want %q", got, "hello")
	}
}

func TestParamsGetMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	if got := p.Get("missing"); got != "" {
		t.Errorf("Get(missing) = %q, want empty", got)
	}
}

func TestParamsGetMultipleReturnsFirst(t *testing.T) {
	p := Params{Query: url.Values{"tag": {"go", "web"}}}
	if got := p.Get("tag"); got != "go" {
		t.Errorf("Get(tag) = %q, want %q", got, "go")
	}
}

func TestParamsInt(t *testing.T) {
	p := Params{Query: url.Values{"page": {"3"}}}
	n, err := p.Int("page")
	if err != nil || n != 3 {
		t.Errorf("Int(page) = (%d, %v), want (3, nil)", n, err)
	}
}

func TestParamsIntMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	_, err := p.Int("page")
	if err == nil {
		t.Error("Int(missing) returned nil error, want error")
	}
}

func TestParamsIntInvalid(t *testing.T) {
	p := Params{Query: url.Values{"page": {"abc"}}}
	_, err := p.Int("page")
	if err == nil {
		t.Error("Int(abc) returned nil error, want error")
	}
}

func TestParamsFloat64(t *testing.T) {
	p := Params{Query: url.Values{"price": {"9.99"}}}
	n, err := p.Float64("price")
	if err != nil || n != 9.99 {
		t.Errorf("Float64(price) = (%f, %v), want (9.99, nil)", n, err)
	}
}

func TestParamsFloat64Missing(t *testing.T) {
	p := Params{Query: url.Values{}}
	_, err := p.Float64("price")
	if err == nil {
		t.Error("Float64(missing) returned nil error, want error")
	}
}

func TestParamsBool(t *testing.T) {
	p := Params{Query: url.Values{
		"yes":   {"true"},
		"no":    {"false"},
		"empty": {""},
	}}
	if !p.Bool("yes") {
		t.Error("Bool(yes) = false, want true")
	}
	if p.Bool("no") {
		t.Error("Bool(no) = true, want false")
	}
	if p.Bool("empty") {
		t.Error("Bool(empty) = true, want false")
	}
	if p.Bool("missing") {
		t.Error("Bool(missing) = true, want false")
	}
}

func TestParamsIntOr(t *testing.T) {
	p := Params{Query: url.Values{"page": {"5"}}}
	if got := p.IntOr("page", 1); got != 5 {
		t.Errorf("IntOr(page, 1) = %d, want 5", got)
	}
}

func TestParamsIntOrMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	if got := p.IntOr("page", 1); got != 1 {
		t.Errorf("IntOr(missing, 1) = %d, want 1", got)
	}
}

func TestParamsIntOrInvalid(t *testing.T) {
	p := Params{Query: url.Values{"page": {"abc"}}}
	if got := p.IntOr("page", 1); got != 1 {
		t.Errorf("IntOr(abc, 1) = %d, want 1", got)
	}
}

func TestParamsFloat64Or(t *testing.T) {
	p := Params{Query: url.Values{"min": {"3.5"}}}
	if got := p.Float64Or("min", 0.0); got != 3.5 {
		t.Errorf("Float64Or(min, 0) = %f, want 3.5", got)
	}
}

func TestParamsFloat64OrMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	if got := p.Float64Or("min", 0.0); got != 0.0 {
		t.Errorf("Float64Or(missing, 0) = %f, want 0", got)
	}
}

func TestParamsFloat64OrInvalid(t *testing.T) {
	p := Params{Query: url.Values{"min": {"abc"}}}
	if got := p.Float64Or("min", 1.5); got != 1.5 {
		t.Errorf("Float64Or(abc, 1.5) = %f, want 1.5", got)
	}
}

func TestParamsBoolOr(t *testing.T) {
	p := Params{Query: url.Values{"drafts": {"true"}}}
	if got := p.BoolOr("drafts", false); !got {
		t.Error("BoolOr(drafts=true, false) = false, want true")
	}
}

func TestParamsBoolOrFalseValue(t *testing.T) {
	p := Params{Query: url.Values{"drafts": {"false"}}}
	if got := p.BoolOr("drafts", true); got {
		t.Error("BoolOr(drafts=false, true) = true, want false")
	}
}

func TestParamsBoolOrMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	if got := p.BoolOr("drafts", true); !got {
		t.Error("BoolOr(missing, true) = false, want true")
	}
}

func TestParamsBoolOrMissingDefaultFalse(t *testing.T) {
	p := Params{Query: url.Values{}}
	if got := p.BoolOr("drafts", false); got {
		t.Error("BoolOr(missing, false) = true, want false")
	}
}

func TestParamsStrings(t *testing.T) {
	p := Params{Query: url.Values{"tag": {"go", "web", "api"}}}
	got := p.Strings("tag")
	if len(got) != 3 || got[0] != "go" || got[1] != "web" || got[2] != "api" {
		t.Errorf("Strings(tag) = %v, want [go web api]", got)
	}
}

func TestParamsStringsMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	if got := p.Strings("tag"); got != nil {
		t.Errorf("Strings(missing) = %v, want nil", got)
	}
}

func TestParamsInts(t *testing.T) {
	p := Params{Query: url.Values{"id": {"1", "2", "3"}}}
	got, err := p.Ints("id")
	if err != nil {
		t.Fatalf("Ints(id) error: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("Ints(id) = %v, want [1 2 3]", got)
	}
}

func TestParamsIntsMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	got, err := p.Ints("id")
	if err != nil || got != nil {
		t.Errorf("Ints(missing) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestParamsIntsInvalid(t *testing.T) {
	p := Params{Query: url.Values{"id": {"1", "bad", "3"}}}
	got, err := p.Ints("id")
	if err == nil {
		t.Fatal("Ints with invalid value returned nil error")
	}
	// Should return values parsed before the error.
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("Ints partial = %v, want [1]", got)
	}
}

func TestParamsFloat64s(t *testing.T) {
	p := Params{Query: url.Values{"v": {"1.1", "2.2"}}}
	got, err := p.Float64s("v")
	if err != nil {
		t.Fatalf("Float64s(v) error: %v", err)
	}
	if len(got) != 2 || got[0] != 1.1 || got[1] != 2.2 {
		t.Errorf("Float64s(v) = %v, want [1.1 2.2]", got)
	}
}

func TestParamsFloat64sMissing(t *testing.T) {
	p := Params{Query: url.Values{}}
	got, err := p.Float64s("v")
	if err != nil || got != nil {
		t.Errorf("Float64s(missing) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestParamsFloat64sInvalid(t *testing.T) {
	p := Params{Query: url.Values{"v": {"1.1", "bad"}}}
	got, err := p.Float64s("v")
	if err == nil {
		t.Fatal("Float64s with invalid value returned nil error")
	}
	if len(got) != 1 || got[0] != 1.1 {
		t.Errorf("Float64s partial = %v, want [1.1]", got)
	}
}

func TestParamsNilQuery(t *testing.T) {
	p := Params{Path: "/test"}

	// All methods should be safe to call with nil Query.
	if got := p.Get("x"); got != "" {
		t.Errorf("Get on nil Query = %q, want empty", got)
	}
	if got := p.IntOr("x", 5); got != 5 {
		t.Errorf("IntOr on nil Query = %d, want 5", got)
	}
	if got := p.BoolOr("x", true); !got {
		t.Error("BoolOr on nil Query = false, want true")
	}
	if got := p.Float64Or("x", 1.0); got != 1.0 {
		t.Errorf("Float64Or on nil Query = %f, want 1.0", got)
	}
	if got := p.Strings("x"); got != nil {
		t.Errorf("Strings on nil Query = %v, want nil", got)
	}
	got, err := p.Ints("x")
	if err != nil || got != nil {
		t.Errorf("Ints on nil Query = (%v, %v), want (nil, nil)", got, err)
	}
}
