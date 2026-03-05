package tether

import (
	"testing"
	"testing/synctest"
)

func TestWatchValueSubscribes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := NewValue(42)
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		w := WatchValue(v, func(val int, s counterState) counterState {
			s.Count = val
			return s
		})
		w.subscribe(sess)
		synctest.Wait()

		if s := sess.State(); s.Count != 42 {
			t.Errorf("Count = %d, want 42", s.Count)
		}

		v.Store(100)
		synctest.Wait()

		if s := sess.State(); s.Count != 100 {
			t.Errorf("Count after Store = %d, want 100", s.Count)
		}
	})
}

func TestWatchBusSubscribes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		w := WatchBus(bus, func(msg string, s counterState) counterState {
			s.Count++
			return s
		})
		w.subscribe(sess)
		synctest.Wait()

		bus.Publish(("hello"))
		synctest.Wait()

		if s := sess.State(); s.Count != 1 {
			t.Errorf("Count = %d, want 1", s.Count)
		}
	})
}
