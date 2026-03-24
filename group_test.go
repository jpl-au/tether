package tether

import (
	"testing"
	"testing/synctest"
)

func TestGroupAddAndLen(t *testing.T) {
	g := NewGroup[counterState]()

	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

	g.Add(sess)
	if g.Len() != 1 {
		t.Errorf("expected Len 1, got %d", g.Len())
	}

	// Adding the same session again is a no-op.
	g.Add(sess)
	if g.Len() != 1 {
		t.Errorf("expected Len 1 after duplicate Add, got %d", g.Len())
	}
}

func TestGroupRemove(t *testing.T) {
	g := NewGroup[counterState]()

	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

	g.Add(sess)
	g.Remove(sess)
	if g.Len() != 0 {
		t.Errorf("expected Len 0 after Remove, got %d", g.Len())
	}

	// Removing a session not in the group is a no-op.
	g.Remove(sess)
}

func TestGroupBroadcastUpdatesAllSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := NewGroup[counterState]()

		ct1 := newConnectedTransport()
		sess1 := newTestSession(counterState{Count: 0}, ct1)

		ct2 := newConnectedTransport()
		sess2 := newTestSession(counterState{Count: 10}, ct2)
		sess2.id = "test-2"

		g.Add(sess1)
		g.Add(sess2)

		go sess1.readTransport(sess1.events)
		go sess1.run()
		go sess2.readTransport(sess2.events)
		go sess2.run()
		defer func() { sess1.stop(); sess2.stop(); synctest.Wait() }()

		g.Broadcast(func(target *StatefulSession[counterState], s counterState) counterState {
			s.Count += 5
			return s
		})

		synctest.Wait()

		if s := sess1.State(); s.Count != 5 {
			t.Errorf("expected sess1 Count 5, got %d", s.Count)
		}
		if s := sess2.State(); s.Count != 15 {
			t.Errorf("expected sess2 Count 15, got %d", s.Count)
		}

		// Both transports should have received an update.
		ct1.mu.Lock()
		if len(ct1.sent) != 1 {
			t.Errorf("expected 1 update on ct1, got %d", len(ct1.sent))
		}
		ct1.mu.Unlock()

		ct2.mu.Lock()
		if len(ct2.sent) != 1 {
			t.Errorf("expected 1 update on ct2, got %d", len(ct2.sent))
		}
		ct2.mu.Unlock()
	})
}

func TestGroupBroadcastEmptyGroupIsNoop(t *testing.T) {
	g := NewGroup[counterState]()

	// Should not panic.
	g.Broadcast(func(target *StatefulSession[counterState], s counterState) counterState {
		s.Count++
		return s
	})
}

func TestGroupOnJoinFires(t *testing.T) {
	g := NewGroup[counterState]()

	var joined *StatefulSession[counterState]
	g.OnJoin = func(s *StatefulSession[counterState]) {
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
	g.OnJoin = func(s *StatefulSession[counterState]) {
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

	var left *StatefulSession[counterState]
	g.OnLeave = func(s *StatefulSession[counterState]) {
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
	g.OnLeave = func(s *StatefulSession[counterState]) {
		callCount++
	}

	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)

	g.Remove(sess) // not in group

	if callCount != 0 {
		t.Errorf("expected OnLeave not to fire, fired %d times", callCount)
	}
}

func TestGroupAll(t *testing.T) {
	g := NewGroup[counterState]()

	mt1 := &mockTransport{events: []Event{}}
	sess1 := newTestSession(counterState{Count: 0}, mt1)

	mt2 := &mockTransport{events: []Event{}}
	sess2 := newTestSession(counterState{Count: 10}, mt2)
	sess2.id = "test-2"

	g.Add(sess1)
	g.Add(sess2)

	ids := map[string]bool{}
	for s := range g.All() {
		ids[s.ID()] = true
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(ids))
	}
	if !ids["test"] || !ids["test-2"] {
		t.Errorf("expected test and test-2 in All(), got %v", ids)
	}
}

func TestGroupAllEmpty(t *testing.T) {
	g := NewGroup[counterState]()
	count := 0
	for range g.All() {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}
}
