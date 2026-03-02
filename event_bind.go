package tether

import (
	"fmt"
	"reflect"
	"strconv"
)

// Bind decodes the event's Data map into a struct. Fields are matched
// by the "tether" struct tag; untagged exported fields use their
// lowercased name. Supported field types: string, int, int64, float64,
// bool.
//
// This is the multi-field counterpart to the single-value helpers
// (Value, Key, Int, Bool). Use Bind when a form submit sends several
// named fields at once:
//
//	var form struct {
//	    Email string `tether:"email"`
//	    Age   int    `tether:"age"`
//	}
//	if err := ev.Bind(&form); err != nil {
//	    s.TodoError = err.Error()
//	    return s
//	}
func (e Event) Bind(dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("tether: Bind requires a non-nil pointer, got %T", dest)
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("tether: Bind requires a pointer to a struct, got *%s", rv.Type())
	}
	rt := rv.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		key := f.Tag.Get("tether")
		if key == "" {
			key = lowercaseFirst(f.Name)
		}
		raw, ok := e.Data[key]
		if !ok {
			continue
		}
		if err := setField(rv.Field(i), raw, key); err != nil {
			return err
		}
	}
	return nil
}

func setField(fv reflect.Value, raw, key string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("tether: field %q: %w", key, err)
		}
		fv.SetInt(n)
	case reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("tether: field %q: %w", key, err)
		}
		fv.SetFloat(n)
	case reflect.Bool:
		fv.SetBool(raw == "true")
	default:
		return fmt.Errorf("tether: unsupported field type %s for key %q", fv.Type(), key)
	}
	return nil
}

// lowercaseFirst returns s with the first byte lowercased. This
// matches the convention of HTML form field names being lowercase
// versions of Go exported field names.
func lowercaseFirst(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}
