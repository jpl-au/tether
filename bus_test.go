package poly

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestBusPublishDeliversToSubscribers(t *testing.T) {
	bus := NewBus[string]()

	var got1, got2 string
	bus.Subscribe(context.Background(), func(ev string) { got1 = ev })
	bus.Subscribe(context.Background(), func(ev string) { got2 = ev })

	bus.Publish("hello")

	if got1 != "hello" {
		t.Errorf("subscriber 1: got %q, want %q", got1, "hello")
	}
	if got2 != "hello" {
		t.Errorf("subscriber 2: got %q, want %q", got2, "hello")
	}
}

func TestBusEmitBuffersDuringHandle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{}, mt)

		var received string
		bus.Subscribe(context.Background(), func(ev string) { received = ev })

		// Simulate being inside Handle.
		sess.handling = true
		sess.fx = &effects{}
		bus.Emit(sess, "buffered")

		if received != "" {
			t.Fatalf("expected no delivery during Handle, got %q", received)
		}

		sess.flushEmissions()

		if received != "buffered" {
			t.Errorf("after flush: got %q, want %q", received, "buffered")
		}
	})
}

func TestBusEmitPublishesOutsideHandle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{}, mt)

		var received string
		bus.Subscribe(context.Background(), func(ev string) { received = ev })

		bus.Emit(sess, "immediate")

		if received != "immediate" {
			t.Errorf("got %q, want %q", received, "immediate")
		}
	})
}

func TestBusSenderFiltering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		mt1 := &mockTransport{events: []Event{}}
		sessA := newTestSession(counterState{}, mt1)
		sessA.id = "A"

		mt2 := &mockTransport{events: []Event{}}
		sessB := newTestSession(counterState{}, mt2)
		sessB.id = "B"

		var gotA, gotB string
		// Subscribe both via the internal subscribe (simulating poly.On).
		bus.subscribe(sessA.ctx, func(ev string) { gotA = ev }, "A")
		bus.subscribe(sessB.ctx, func(ev string) { gotB = ev }, "B")

		// Emit from session A — A should be skipped, B should receive.
		bus.Emit(sessA, "from-A")

		if gotA != "" {
			t.Errorf("sender A should be filtered, got %q", gotA)
		}
		if gotB != "from-A" {
			t.Errorf("subscriber B: got %q, want %q", gotB, "from-A")
		}
	})
}

func TestBusSubscribeAutoCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		ctx, cancel := context.WithCancel(context.Background())
		var received string
		bus.Subscribe(ctx, func(ev string) { received = ev })

		if bus.Len() != 1 {
			t.Fatalf("Len() = %d, want 1", bus.Len())
		}

		cancel()
		synctest.Wait()

		if bus.Len() != 0 {
			t.Fatalf("Len() = %d after cancel, want 0", bus.Len())
		}

		bus.Publish("after-cancel")

		if received != "" {
			t.Errorf("cancelled subscriber received %q", received)
		}
	})
}

func TestBusPublishNoSenderFilter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{}, mt)

		var got string
		// Subscribe with session ID (as poly.On would).
		bus.subscribe(sess.ctx, func(ev string) { got = ev }, sess.id)

		// Publish (no sender) — should deliver to everyone.
		bus.Publish("broadcast")

		if got != "broadcast" {
			t.Errorf("got %q, want %q", got, "broadcast")
		}
	})
}

func TestBusLen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[int]()

		if bus.Len() != 0 {
			t.Fatalf("empty bus Len() = %d", bus.Len())
		}

		ctx1, cancel1 := context.WithCancel(context.Background())
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()

		bus.Subscribe(ctx1, func(int) {})
		bus.Subscribe(ctx2, func(int) {})

		if bus.Len() != 2 {
			t.Fatalf("Len() = %d, want 2", bus.Len())
		}

		cancel1()
		synctest.Wait()

		if bus.Len() != 1 {
			t.Fatalf("Len() after cancel = %d, want 1", bus.Len())
		}
	})
}

func TestBusUnsubscribe(t *testing.T) {
	bus := NewBus[string]()

	var received string
	unsub := bus.Subscribe(context.Background(), func(ev string) { received = ev })

	bus.Publish("before")
	if received != "before" {
		t.Fatalf("got %q, want %q", received, "before")
	}

	unsub()
	received = ""
	bus.Publish("after")

	if received != "" {
		t.Errorf("unsubscribed callback received %q", received)
	}
}
