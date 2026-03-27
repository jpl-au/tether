package tether

import "testing"

func TestVersionedZeroValue(t *testing.T) {
	var v Versioned[string]
	if v.Version() != 0 {
		t.Errorf("zero value version should be 0, got %d", v.Version())
	}
	if v.Val != "" {
		t.Errorf("zero value Val should be empty, got %q", v.Val)
	}
}

func TestNewVersioned(t *testing.T) {
	v := NewVersioned([]int{1, 2, 3})
	if v.Version() != 1 {
		t.Errorf("NewVersioned version should be 1, got %d", v.Version())
	}
	if len(v.Val) != 3 {
		t.Errorf("NewVersioned Val should have 3 items, got %d", len(v.Val))
	}
}

func TestVersionedWithIncrementsVersion(t *testing.T) {
	v := NewVersioned([]string{"a"})
	v2 := v.With(append(v.Val, "b"))

	if v.Version() != 1 {
		t.Error("original should not be modified")
	}
	if v2.Version() != 2 {
		t.Errorf("With should increment version to 2, got %d", v2.Version())
	}
	if len(v2.Val) != 2 {
		t.Errorf("With should update data, got %d items", len(v2.Val))
	}
}

func TestVersionedWithChain(t *testing.T) {
	v := NewVersioned(0)
	v = v.With(1)
	v = v.With(2)
	v = v.With(3)

	if v.Version() != 4 {
		t.Errorf("three With calls from version 1 should give 4, got %d", v.Version())
	}
	if v.Val != 3 {
		t.Errorf("Val should be 3, got %d", v.Val)
	}
}

func TestVersionedValueSemantics(t *testing.T) {
	v1 := NewVersioned("hello")
	v2 := v1 // copy
	v2 = v2.With("world")

	if v1.Val != "hello" {
		t.Error("v1 should not be affected by v2 mutation")
	}
	if v1.Version() != 1 {
		t.Error("v1 version should not change")
	}
	if v2.Val != "world" {
		t.Error("v2 should have new data")
	}
	if v2.Version() != 2 {
		t.Error("v2 should have incremented version")
	}
}

func TestVersionedWithFromZero(t *testing.T) {
	var v Versioned[int]
	v = v.With(42)

	if v.Version() != 1 {
		t.Errorf("With from zero value should give version 1, got %d", v.Version())
	}
	if v.Val != 42 {
		t.Errorf("Val should be 42, got %d", v.Val)
	}
}
