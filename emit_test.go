package poly

import (
	"testing"
	"testing/synctest"

	"github.com/jpl-au/fluent-poly/event"
)

func TestOnSubscribesSessionToBus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		On(bus, sess, func(ev string, s counterState) counterState {
			s.Count += len(ev)
			return s
		})
		synctest.Wait()

		bus.Publish("hi")
		synctest.Wait()

		if s := sess.State(); s.Count != 2 {
			t.Errorf("Count = %d, want 2", s.Count)
		}
	})
}

func TestOnAutoCleanupOnSessionDestroy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()

		On(bus, sess, func(ev string, s counterState) counterState {
			s.Count++
			return s
		})
		synctest.Wait()

		if bus.Len() != 1 {
			t.Fatalf("Len() = %d, want 1", bus.Len())
		}

		sess.stop()
		synctest.Wait()

		if bus.Len() != 0 {
			t.Fatalf("Len() after stop = %d, want 0", bus.Len())
		}
	})
}

func TestEmitAndOnEndToEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type msg struct{ Text string }
		bus := NewBus[msg]()

		// Session A: the sender.
		mtA := &mockTransport{
			events: []Event{
				{Type: event.Click, Action: "send"},
			},
		}
		sessA := newTestSession(counterState{Count: 0}, mtA)
		sessA.id = "sender"
		sessA.handle = func(_ *Session[counterState], s counterState, ev Event) counterState {
			if ev.Action == "send" {
				s.Count += 10
				bus.Emit(sessA, msg{Text: "hello"})
			}
			return s
		}

		// Session B: the receiver.
		mtB := &mockTransport{events: []Event{}}
		sessB := newTestSession(counterState{Count: 0}, mtB)
		sessB.id = "receiver"

		go sessA.readTransport(sessA.events)
		go sessA.run()
		defer func() { sessA.stop(); synctest.Wait() }()

		go sessB.readTransport(sessB.events)
		go sessB.run()
		defer func() { sessB.stop(); synctest.Wait() }()

		// Subscribe both. Sender filtering should prevent A from
		// receiving its own event.
		On(bus, sessA, func(ev msg, s counterState) counterState {
			s.Count += 100 // should NOT fire for A's own emit
			return s
		})
		On(bus, sessB, func(ev msg, s counterState) counterState {
			s.Count += 1 // should fire
			return s
		})
		synctest.Wait()

		// Wait for A's event to be processed (triggers emit + flush).
		synctest.Wait()

		stateA := sessA.State()
		if stateA.Count != 10 {
			t.Errorf("sender Count = %d, want 10 (handle only, no self-delivery)", stateA.Count)
		}

		stateB := sessB.State()
		if stateB.Count != 1 {
			t.Errorf("receiver Count = %d, want 1", stateB.Count)
		}
	})
}

func TestEmitInsideUpdateCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[int]()
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		var received int
		bus.Subscribe(sess.ctx, func(ev int) { received = ev })
		synctest.Wait()

		sess.Update(func(s counterState) counterState {
			s.Count = 42
			bus.Emit(sess, 42)
			return s
		})
		synctest.Wait()

		if received != 42 {
			t.Errorf("received = %d, want 42", received)
		}
	})
}
