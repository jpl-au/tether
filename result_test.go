package poly

import "testing"

func TestHandleResultAnnounceMergedIntoUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "increment"},
		},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.handle = func(s counterState, ev Event) HandleResult[counterState] {
		s.Count++
		return Result(s).WithAnnounce("Counter incremented")
	}

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].Announce != "Counter incremented" {
		t.Errorf("Announce = %q, want %q", mt.updates[0].Announce, "Counter incremented")
	}
	if len(mt.updates[0].Patches) != 1 {
		t.Errorf("expected 1 patch alongside announce, got %d", len(mt.updates[0].Patches))
	}
}

func TestHandleResultFlashMergedIntoUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "increment"},
		},
	}

	sess := newTestSession(counterState{Count: 0}, mt)
	sess.handle = func(s counterState, ev Event) HandleResult[counterState] {
		s.Count++
		return Result(s).WithFlash("#notice", "Saved!")
	}

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mt.updates))
	}
	if mt.updates[0].Flash["#notice"] != "Saved!" {
		t.Errorf("Flash[#notice] = %q, want %q", mt.updates[0].Flash["#notice"], "Saved!")
	}
}

func TestHandleResultNoEffectsNoExtraUpdate(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "noop"},
		},
	}

	sess := newTestSession(counterState{Count: 5}, mt)
	sess.equal = func(a, b counterState) bool { return a.Count == b.Count }

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 0 {
		t.Errorf("expected no updates for noop with Equal, got %d", len(mt.updates))
	}
}

func TestHandleResultEffectsSentEvenWhenStateUnchanged(t *testing.T) {
	mt := &mockTransport{
		events: []Event{
			{Type: "click", Action: "noop"},
		},
	}

	sess := newTestSession(counterState{Count: 5}, mt)
	sess.equal = func(a, b counterState) bool { return a.Count == b.Count }
	sess.handle = func(s counterState, ev Event) HandleResult[counterState] {
		return Result(s).WithAnnounce("Still here")
	}

	sess.run()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if len(mt.updates) != 1 {
		t.Fatalf("expected 1 update for effects despite equal state, got %d", len(mt.updates))
	}
	if mt.updates[0].Announce != "Still here" {
		t.Errorf("Announce = %q, want %q", mt.updates[0].Announce, "Still here")
	}
}
