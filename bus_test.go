package tether

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

func TestBusEmitDefersPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		var received string
		bus.Subscribe(context.Background(), func(ev string) { received = ev })

		// Emit enqueues the publication - it runs after the loop
		// processes the command, not immediately.
		bus.Emit(sess, "deferred")
		synctest.Wait()

		if received != "deferred" {
			t.Errorf("got %q, want %q", received, "deferred")
		}
	})
}

func TestBusEmitPublishesOutsideHandle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		var received string
		bus.Subscribe(context.Background(), func(ev string) { received = ev })

		bus.Emit(sess, "immediate")
		synctest.Wait()

		if received != "immediate" {
			t.Errorf("got %q, want %q", received, "immediate")
		}
	})
}

func TestBusSenderFiltering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		ct1 := newConnectedTransport()
		sessA := newTestSession(counterState{}, ct1)
		sessA.id = "A"

		ct2 := newConnectedTransport()
		sessB := newTestSession(counterState{}, ct2)
		sessB.id = "B"

		go sessA.readTransport(sessA.events)
		go sessA.run()
		defer func() { sessA.stop(); synctest.Wait() }()

		go sessB.readTransport(sessB.events)
		go sessB.run()
		defer func() { sessB.stop(); synctest.Wait() }()

		var gotA, gotB string
		// Subscribe both via the internal subscribe (simulating tether.On).
		bus.subscribe(sessA.ctx, func(ev string) { gotA = ev }, "A")
		bus.subscribe(sessB.ctx, func(ev string) { gotB = ev }, "B")

		// Emit from session A - A should be skipped, B should receive.
		bus.Emit(sessA, "from-A")
		synctest.Wait()

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

		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()

		var got string
		// Subscribe with session ID (as tether.On would).
		bus.subscribe(sess.ctx, func(ev string) { got = ev }, sess.id)

		// Publish (no sender) - should deliver to everyone.
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

func TestBusSubscribeAsyncDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		var got string
		bus.SubscribeAsync(context.Background(), func(ev string) { got = ev })

		bus.Publish("hello")
		synctest.Wait()

		if got != "hello" {
			t.Errorf("async subscriber: got %q, want %q", got, "hello")
		}
	})
}

func TestBusSubscribeAsyncNonBlocking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		blocked := make(chan struct{})
		bus.SubscribeAsync(context.Background(), func(string) {
			<-blocked // block until released
		})

		// A sync subscriber records delivery order.
		var syncGot string
		bus.Subscribe(context.Background(), func(ev string) { syncGot = ev })

		// Publish returns without waiting for the async callback.
		bus.Publish("fast")

		if syncGot != "fast" {
			t.Errorf("sync subscriber not called: got %q, want %q", syncGot, "fast")
		}

		close(blocked)
		synctest.Wait()
	})
}

func TestBusSubscribeAsyncAutoCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		ctx, cancel := context.WithCancel(context.Background())
		var received string
		bus.SubscribeAsync(ctx, func(ev string) { received = ev })

		if bus.Len() != 1 {
			t.Fatalf("Len() = %d, want 1", bus.Len())
		}

		cancel()
		synctest.Wait()

		if bus.Len() != 0 {
			t.Fatalf("Len() = %d after cancel, want 0", bus.Len())
		}

		bus.Publish("after-cancel")
		synctest.Wait()

		if received != "" {
			t.Errorf("cancelled async subscriber received %q", received)
		}
	})
}

func TestBusSubscribeAsyncUnsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[string]()

		var received string
		unsub := bus.SubscribeAsync(context.Background(), func(ev string) { received = ev })

		bus.Publish("before")
		synctest.Wait()

		if received != "before" {
			t.Fatalf("got %q, want %q", received, "before")
		}

		unsub()
		received = ""
		bus.Publish("after")
		synctest.Wait()

		if received != "" {
			t.Errorf("unsubscribed async callback received %q", received)
		}
	})
}
