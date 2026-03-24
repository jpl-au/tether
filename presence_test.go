package tether

import "testing"

func TestPresenceSetAndGet(t *testing.T) {
	p := NewPresence[string]()
	p.Set("s1", "viewing card-1")

	v, ok := p.Get("s1")
	if !ok {
		t.Fatal("expected s1 to be present")
	}
	if v != "viewing card-1" {
		t.Errorf("Get(s1) = %q, want %q", v, "viewing card-1")
	}
}

func TestPresenceGetMissing(t *testing.T) {
	p := NewPresence[string]()

	_, ok := p.Get("absent")
	if ok {
		t.Error("expected absent key to return false")
	}
}

func TestPresenceClear(t *testing.T) {
	p := NewPresence[string]()
	p.Set("s1", "data")
	p.Clear("s1")

	_, ok := p.Get("s1")
	if ok {
		t.Error("expected s1 to be cleared")
	}
	if p.Len() != 0 {
		t.Errorf("Len() = %d, want 0", p.Len())
	}
}

func TestPresenceClearAbsentIsNoop(t *testing.T) {
	p := NewPresence[string]()
	p.Clear("nonexistent") // should not panic
}

func TestPresenceLen(t *testing.T) {
	p := NewPresence[int]()

	if p.Len() != 0 {
		t.Errorf("empty Len() = %d, want 0", p.Len())
	}

	p.Set("a", 1)
	p.Set("b", 2)

	if p.Len() != 2 {
		t.Errorf("Len() = %d, want 2", p.Len())
	}
}

func TestPresenceSetOverwrites(t *testing.T) {
	p := NewPresence[string]()
	p.Set("s1", "old")
	p.Set("s1", "new")

	v, _ := p.Get("s1")
	if v != "new" {
		t.Errorf("Get(s1) = %q, want %q", v, "new")
	}
	if p.Len() != 1 {
		t.Errorf("Len() = %d, want 1 after overwrite", p.Len())
	}
}

func TestPresenceAll(t *testing.T) {
	p := NewPresence[string]()
	p.Set("s1", "a")
	p.Set("s2", "b")

	all := p.All()
	if len(all) != 2 {
		t.Fatalf("All() has %d entries, want 2", len(all))
	}
	if all["s1"] != "a" || all["s2"] != "b" {
		t.Errorf("All() = %v, want s1=a s2=b", all)
	}
}

func TestPresenceAllIsSnapshot(t *testing.T) {
	p := NewPresence[string]()
	p.Set("s1", "before")

	snap := p.All()
	p.Set("s1", "after")

	if snap["s1"] != "before" {
		t.Error("All() should return a snapshot, not a live reference")
	}
}

func TestPresenceEach(t *testing.T) {
	p := NewPresence[string]()
	p.Set("s1", "alice")
	p.Set("s2", "bob")
	p.Set("s3", "carol")

	// Exclude s2, collect the rest.
	var names []string
	p.Each("s2", func(_ string, name string) {
		names = append(names, name)
	})

	if len(names) != 2 {
		t.Fatalf("Each(exclude s2) returned %d entries, want 2", len(names))
	}

	has := map[string]bool{}
	for _, n := range names {
		has[n] = true
	}
	if !has["alice"] || !has["carol"] {
		t.Errorf("Each(exclude s2) = %v, want alice and carol", names)
	}
	if has["bob"] {
		t.Error("Each should have excluded bob (s2)")
	}
}

func TestPresenceEachEmptyExclude(t *testing.T) {
	p := NewPresence[string]()
	p.Set("s1", "alice")

	count := 0
	p.Each("", func(_ string, _ string) {
		count++
	})

	if count != 1 {
		t.Errorf("Each with empty exclude should include all, got %d", count)
	}
}

func TestPresenceEachEmpty(t *testing.T) {
	p := NewPresence[string]()

	count := 0
	p.Each("", func(_ string, _ string) {
		count++
	})

	if count != 0 {
		t.Errorf("Each on empty presence should yield 0, got %d", count)
	}
}
