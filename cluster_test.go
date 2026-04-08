package tether

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/fxamacker/cbor/v2"
)

// memCluster is an in-memory Cluster implementation for testing.
// Publish routes messages directly to registered subscriber callbacks.
type memCluster struct {
	mu   sync.Mutex
	subs map[string][]func([]byte)
}

func newMemCluster() *memCluster {
	return &memCluster{subs: make(map[string][]func([]byte))}
}

func (mc *memCluster) Publish(_ context.Context, topic string, data []byte) error {
	mc.mu.Lock()
	fns := make([]func([]byte), len(mc.subs[topic]))
	copy(fns, mc.subs[topic])
	mc.mu.Unlock()
	for _, fn := range fns {
		fn(data)
	}
	return nil
}

func (mc *memCluster) Subscribe(topic string, fn func([]byte)) func() {
	mc.mu.Lock()
	mc.subs[topic] = append(mc.subs[topic], fn)
	mc.mu.Unlock()
	// Unsubscribe is a no-op for these tests - each test creates a
	// fresh memCluster so cleanup happens via resetCluster.
	return func() {}
}

// mustCBOR encodes v as CBOR, panicking on error. Keeps test setup concise.
func mustCBOR(v any) []byte {
	data, err := cbor.Marshal(v)
	if err != nil {
		panic("mustCBOR: " + err.Error())
	}
	return data
}

// mustCBOREnvelope encodes an envelope struct as CBOR via marshalEnvelopeCBOR,
// panicking on error.
func mustCBOREnvelope(v any) []byte {
	data, err := marshalEnvelopeCBOR(v)
	if err != nil {
		panic("mustCBOREnvelope: " + err.Error())
	}
	return data
}

// --- Bus cluster tests ---

func TestBusEmptyTopicNoCluster(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		// A bus without a topic should not interact with the cluster.
		bus := NewBus[string]()

		var received string
		bus.Subscribe(context.Background(), func(ev string) { received = ev })
		bus.Publish("local-only")

		if received != "local-only" {
			t.Errorf("local subscriber got %q, want %q", received, "local-only")
		}

		// The cluster should have no subscriptions.
		mc.mu.Lock()
		count := len(mc.subs)
		mc.mu.Unlock()
		if count != 0 {
			t.Errorf("cluster has %d subscriptions, want 0 for a topic-less bus", count)
		}
	})
}

func TestBusWithTopicPublishesToCluster(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		// Track what the cluster receives.
		var published []byte
		mc.Subscribe("tether:bus:test-bus", func(data []byte) {
			published = data
		})

		bus := NewBus[string](BusConfig{Topic: "test-bus"})
		defer unregisterTopic("tether:bus:test-bus")

		bus.Publish("cluster-event")

		if published == nil {
			t.Fatal("cluster received no data, expected an envelope")
		}

		env, err := unmarshalBusEnvelope(published)
		if err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.NodeID != nodeID {
			t.Errorf("envelope nodeID = %q, want %q", env.NodeID, nodeID)
		}

		var event string
		if err := cbor.Unmarshal(env.Data, &event); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		if event != "cluster-event" {
			t.Errorf("event = %q, want %q", event, "cluster-event")
		}
	})
}

func TestBusSelfFiltering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		bus := NewBus[string](BusConfig{Topic: "self-filter"})
		defer unregisterTopic("tether:bus:self-filter")

		var received []string
		bus.Subscribe(context.Background(), func(ev string) {
			received = append(received, ev)
		})

		// Trigger the cluster subscription by publishing.
		bus.Publish("trigger")
		synctest.Wait()

		// Simulate a cluster message from this node. The bus should
		// ignore it because the nodeID matches.
		env := busEnvelope{
			NodeID: nodeID,
			Data:   mustCBOR("from-self"),
		}
		mc.Publish(context.Background(), "tether:bus:self-filter", mustCBOREnvelope(env))
		synctest.Wait()

		// Only the direct publish should have been received.
		if len(received) != 1 {
			t.Fatalf("received %d events, want 1 (self-message should be filtered)", len(received))
		}
		if received[0] != "trigger" {
			t.Errorf("received[0] = %q, want %q", received[0], "trigger")
		}
	})
}

func TestBusCrossNodeDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		bus := NewBus[string](BusConfig{Topic: "cross-node"})
		defer unregisterTopic("tether:bus:cross-node")

		var received []string
		bus.Subscribe(context.Background(), func(ev string) {
			received = append(received, ev)
		})

		// Trigger the cluster subscription.
		bus.Publish("local")
		synctest.Wait()

		// Inject an envelope from a different node.
		env := busEnvelope{
			NodeID: "remote-node-abc",
			Data:   mustCBOR("from-remote"),
		}
		mc.Publish(context.Background(), "tether:bus:cross-node", mustCBOREnvelope(env))
		synctest.Wait()

		if len(received) != 2 {
			t.Fatalf("received %d events, want 2", len(received))
		}
		if received[0] != "local" {
			t.Errorf("received[0] = %q, want %q", received[0], "local")
		}
		if received[1] != "from-remote" {
			t.Errorf("received[1] = %q, want %q", received[1], "from-remote")
		}
	})
}

// --- Value cluster tests ---

func TestValueWithTopicPublishesToCluster(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		var published []byte
		mc.Subscribe("tether:value:test-val", func(data []byte) {
			published = data
		})

		v := NewValue(0, "test-val")
		defer unregisterTopic("tether:value:test-val")

		v.Store(42)

		if published == nil {
			t.Fatal("cluster received no data, expected an envelope")
		}

		env, err := unmarshalValueEnvelope(published)
		if err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.NodeID != nodeID {
			t.Errorf("envelope nodeID = %q, want %q", env.NodeID, nodeID)
		}

		var val int
		if err := cbor.Unmarshal(env.Data, &val); err != nil {
			t.Fatalf("unmarshal value: %v", err)
		}
		if val != 42 {
			t.Errorf("value = %d, want 42", val)
		}
	})
}

func TestValueSelfFiltering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		v := NewValue(0, "val-self-filter")
		defer unregisterTopic("tether:value:val-self-filter")

		var observed []int
		v.bus.Subscribe(context.Background(), func(val int) {
			observed = append(observed, val)
		})

		// Store triggers the cluster subscription and publishes.
		v.Store(10)
		synctest.Wait()

		// Simulate a cluster message from this node. Should be ignored.
		env := valueEnvelope{
			NodeID: nodeID,
			Data:   mustCBOR(99),
		}
		mc.Publish(context.Background(), "tether:value:val-self-filter", mustCBOREnvelope(env))
		synctest.Wait()

		// Only the direct store should have been observed.
		if len(observed) != 1 {
			t.Fatalf("observed %d values, want 1 (self-message should be filtered)", len(observed))
		}
		if observed[0] != 10 {
			t.Errorf("observed[0] = %d, want 10", observed[0])
		}
	})
}

func TestValueCrossNodeDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		v := NewValue(0, "val-cross-node")
		defer unregisterTopic("tether:value:val-cross-node")

		var observed []int
		v.bus.Subscribe(context.Background(), func(val int) {
			observed = append(observed, val)
		})

		// Store triggers the cluster subscription.
		v.Store(1)
		synctest.Wait()

		// Inject an envelope from a different node.
		env := valueEnvelope{
			NodeID: "remote-node-xyz",
			Data:   mustCBOR(77),
		}
		mc.Publish(context.Background(), "tether:value:val-cross-node", mustCBOREnvelope(env))
		synctest.Wait()

		if len(observed) != 2 {
			t.Fatalf("observed %d values, want 2", len(observed))
		}
		if observed[1] != 77 {
			t.Errorf("observed[1] = %d, want 77", observed[1])
		}

		// The stored value should reflect the remote update.
		if got := v.Load(); got != 77 {
			t.Errorf("Load() = %d, want 77 after remote update", got)
		}
	})
}

func TestValueWithoutTopicNoCluster(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		// A value without a topic should not interact with the cluster.
		v := NewValue(0)

		v.Store(5)

		mc.mu.Lock()
		count := len(mc.subs)
		mc.mu.Unlock()
		if count != 0 {
			t.Errorf("cluster has %d subscriptions, want 0 for a topic-less value", count)
		}

		if got := v.Load(); got != 5 {
			t.Errorf("Load() = %d, want 5", got)
		}
	})
}

// --- Topic registration tests ---

func TestDuplicateTopicPanics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		_ = NewBus[string](BusConfig{Topic: "dup-bus"})
		defer unregisterTopic("tether:bus:dup-bus")

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic for duplicate bus topic, got none")
			}
		}()

		// Creating a second bus with the same topic should panic.
		_ = NewBus[int](BusConfig{Topic: "dup-bus"})
	})
}

func TestDuplicateValueTopicPanics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		_ = NewValue(0, "dup-val")
		defer unregisterTopic("tether:value:dup-val")

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic for duplicate value topic, got none")
			}
		}()

		// Creating a second value with the same topic should panic.
		_ = NewValue("", "dup-val")
	})
}

func TestBusAndValueTopicNamespacesAreSeparate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer resetCluster()
		mc := newMemCluster()
		SetCluster(mc)

		// A bus topic "foo" registers as "tether:bus:foo" and a value
		// topic "foo" registers as "tether:value:foo". These should not
		// conflict because they occupy different namespaces.
		bus := NewBus[string](BusConfig{Topic: "foo"})
		defer unregisterTopic("tether:bus:foo")

		val := NewValue(0, "foo")
		defer unregisterTopic("tether:value:foo")

		// Verify both work independently.
		var busReceived string
		bus.Subscribe(context.Background(), func(ev string) { busReceived = ev })
		bus.Publish("bus-event")

		if busReceived != "bus-event" {
			t.Errorf("bus subscriber got %q, want %q", busReceived, "bus-event")
		}

		val.Store(123)
		if got := val.Load(); got != 123 {
			t.Errorf("value Load() = %d, want 123", got)
		}
	})
}
