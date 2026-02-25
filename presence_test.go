package poly

import (
	"testing"
)

func TestGroupOnJoinFires(t *testing.T) {
	g := NewGroup[counterState]()

	var joined *Session[counterState]
	g.OnJoin = func(s *Session[counterState]) {
		joined = s
	}

	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

	g.Add(sess)

	if joined != sess {
		t.Error("expected OnJoin to fire with the added session")
	}
}

func TestGroupOnJoinDoesNotFireForDuplicate(t *testing.T) {
	g := NewGroup[counterState]()

	callCount := 0
	g.OnJoin = func(s *Session[counterState]) {
		callCount++
	}

	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

	g.Add(sess)
	g.Add(sess) // duplicate

	if callCount != 1 {
		t.Errorf("expected OnJoin to fire once, fired %d times", callCount)
	}
}

func TestGroupOnLeaveFires(t *testing.T) {
	g := NewGroup[counterState]()

	var left *Session[counterState]
	g.OnLeave = func(s *Session[counterState]) {
		left = s
	}

	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

	g.Add(sess)
	g.Remove(sess)

	if left != sess {
		t.Error("expected OnLeave to fire with the removed session")
	}
}

func TestGroupOnLeaveDoesNotFireForAbsent(t *testing.T) {
	g := NewGroup[counterState]()

	callCount := 0
	g.OnLeave = func(s *Session[counterState]) {
		callCount++
	}

	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

	g.Remove(sess) // not in group

	if callCount != 0 {
		t.Errorf("expected OnLeave not to fire, fired %d times", callCount)
	}
}

func TestGroupMembers(t *testing.T) {
	g := NewGroup[counterState]()

	mt1 := &mockTransport{events: []Event{}}
	sess1 := newTestSession(counterState{Count: 0}, mt1)

	mt2 := &mockTransport{events: []Event{}}
	sess2 := newTestSession(counterState{Count: 10}, mt2)
	sess2.id = "test-2"

	g.Add(sess1)
	g.Add(sess2)

	members := g.Members()
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}

	ids := map[string]bool{}
	for _, m := range members {
		ids[m.ID()] = true
	}
	if !ids["test"] || !ids["test-2"] {
		t.Errorf("expected test and test-2 in members, got %v", ids)
	}
}

func TestGroupMembersEmpty(t *testing.T) {
	g := NewGroup[counterState]()
	members := g.Members()
	if len(members) != 0 {
		t.Errorf("expected 0 members, got %d", len(members))
	}
}
