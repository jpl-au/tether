package tetheredis

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestClient(t *testing.T, addr string) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

func TestPublishSubscribe(t *testing.T) {
	mr := miniredis.RunT(t)

	c := New(newTestClient(t, mr.Addr()))

	var (
		mu  sync.Mutex
		got []byte
	)
	done := make(chan struct{})

	unsub := c.Subscribe("topic", func(data []byte) {
		mu.Lock()
		got = data
		mu.Unlock()
		close(done)
	})
	defer unsub()

	if err := c.Publish(context.Background(), "topic", []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	mu.Lock()
	defer mu.Unlock()
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestCrossNodeDelivery(t *testing.T) {
	mr := miniredis.RunT(t)

	// Two separate Cluster instances simulating two server processes
	// connected to the same Redis.
	node1 := New(newTestClient(t, mr.Addr()))
	node2 := New(newTestClient(t, mr.Addr()))

	var (
		mu  sync.Mutex
		got []byte
	)
	done := make(chan struct{})

	unsub := node2.Subscribe("cross", func(data []byte) {
		mu.Lock()
		got = data
		mu.Unlock()
		close(done)
	})
	defer unsub()

	// Publish from node1; node2 should receive it.
	if err := node1.Publish(context.Background(), "cross", []byte("from-node1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cross-node message")
	}

	mu.Lock()
	defer mu.Unlock()
	if string(got) != "from-node1" {
		t.Errorf("got %q, want %q", got, "from-node1")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	mr := miniredis.RunT(t)

	c1 := New(newTestClient(t, mr.Addr()))
	c2 := New(newTestClient(t, mr.Addr()))
	c3 := New(newTestClient(t, mr.Addr()))
	publisher := New(newTestClient(t, mr.Addr()))

	var wg sync.WaitGroup
	wg.Add(3)

	results := make([]string, 3)

	for i, c := range []*Cluster{c1, c2, c3} {
		unsub := c.Subscribe("multi", func(data []byte) {
			results[i] = string(data)
			wg.Done()
		})
		defer unsub()
	}

	if err := publisher.Publish(context.Background(), "multi", []byte("broadcast")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ch := make(chan struct{})
	go func() {
		wg.Wait()
		close(ch)
	}()

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for all subscribers")
	}

	for i, got := range results {
		if got != "broadcast" {
			t.Errorf("subscriber %d: got %q, want %q", i, got, "broadcast")
		}
	}
}

func TestUnsubscribe(t *testing.T) {
	mr := miniredis.RunT(t)

	c := New(newTestClient(t, mr.Addr()))
	publisher := New(newTestClient(t, mr.Addr()))

	received := make(chan string, 10)

	unsub := c.Subscribe("unsub-topic", func(data []byte) {
		received <- string(data)
	})

	// First message should arrive.
	if err := publisher.Publish(context.Background(), "unsub-topic", []byte("before")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-received:
		if got != "before" {
			t.Errorf("got %q, want %q", got, "before")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first message")
	}

	// Unsubscribe and wait briefly for the close to propagate.
	unsub()
	time.Sleep(100 * time.Millisecond)

	// Publish again - should not be received.
	if err := publisher.Publish(context.Background(), "unsub-topic", []byte("after")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Give a short window for any errant delivery.
	select {
	case got := <-received:
		t.Errorf("received %q after unsubscribe, want nothing", got)
	case <-time.After(200 * time.Millisecond):
		// Expected: no message received.
	}
}
