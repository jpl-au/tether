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

func TestBusAsyncOverflowBlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Semaphore of 1 so the second publish must wait.
		bus := NewBus[int](BusConfig{AsyncWorkers: 1, AsyncOverflow: Block})

		gate := make(chan struct{})
		var count int
		bus.SubscribeAsync(context.Background(), func(int) {
			<-gate
			count++
		})

		bus.Publish(1) // fills the single slot
		synctest.Wait()

		// Publish in a goroutine because Block will stall.
		go bus.Publish(2)
		synctest.Wait()

		// First callback still blocked, second queued.
		if count != 0 {
			t.Fatalf("count = %d, want 0 (callbacks blocked)", count)
		}

		close(gate)
		synctest.Wait()

		if count != 2 {
			t.Errorf("count = %d, want 2 (both delivered)", count)
		}
	})
}

func TestBusAsyncOverflowDrop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[int](BusConfig{AsyncWorkers: 1, AsyncOverflow: Drop})

		gate := make(chan struct{})
		var count int
		bus.SubscribeAsync(context.Background(), func(int) {
			<-gate
			count++
		})

		bus.Publish(1) // fills the single slot
		bus.Publish(2) // semaphore full, dropped

		close(gate)
		synctest.Wait()

		if count != 1 {
			t.Errorf("count = %d, want 1 (second event dropped)", count)
		}
	})
}

func TestBusAsyncOverflowInline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewBus[int](BusConfig{AsyncWorkers: 1, AsyncOverflow: Inline})

		gate := make(chan struct{})
		var results []string
		bus.SubscribeAsync(context.Background(), func(v int) {
			if v == 1 {
				<-gate // block first callback
			}
			results = append(results, "async")
		})

		bus.Publish(1) // fills the single slot
		synctest.Wait()

		// Second publish runs inline because semaphore is full.
		// We use a sync subscriber to verify ordering.
		var syncRan bool
		bus.Subscribe(context.Background(), func(int) { syncRan = true })
		bus.Publish(2)

		if !syncRan {
			t.Fatal("sync subscriber did not run")
		}
		// The inline execution should have appended before Publish returned.
		if len(results) != 1 || results[0] != "async" {
			t.Errorf("results = %v, want [async] (inline execution)", results)
		}

		close(gate)
		synctest.Wait()

		if len(results) != 2 {
			t.Errorf("len(results) = %d, want 2", len(results))
		}
	})
}
