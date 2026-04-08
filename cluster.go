package tether

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/jpl-au/tether/dev"
)

// Cluster enables cross-node communication for Bus and Value. When a
// Cluster is configured on [App], any Bus or Value created with a topic
// name publishes state changes to the cluster and subscribes to
// changes from other nodes. Local-only buses and values (empty topic)
// are unaffected.
//
// Implement this interface to integrate with your messaging
// infrastructure (Redis Pub/Sub, NATS, etc.). The framework handles
// serialisation, self-filtering, and topic naming - implementations
// only need to move bytes between nodes.
type Cluster interface {
	// Publish sends data to all subscribers of the given topic.
	// Implementations should be fire-and-forget from the caller's
	// perspective - errors are logged internally, not returned.
	Publish(ctx context.Context, topic string, data []byte) error

	// Subscribe registers a callback for messages on the given topic.
	// Returns an unsubscribe function. Subscribe is called at startup
	// and must not fail silently - panic on subscription errors so
	// they surface immediately, like duplicate HTTP routes.
	Subscribe(topic string, fn func(data []byte)) func()
}

// cluster is the package-level cluster instance, set by SetCluster
// during App initialisation. Buses and Values discover it here.
var cluster Cluster

// nodeID uniquely identifies this process. Used for self-filtering
// so a node does not process its own cluster messages.
var nodeID string

// topicRegistry tracks registered topic names to detect duplicates.
// Duplicate topics panic at startup, like duplicate HTTP routes.
var topicRegistry = struct {
	mu     sync.Mutex
	topics map[string]bool
}{topics: make(map[string]bool)}

func init() {
	nodeID = generateNodeID()
}

// SetCluster configures the package-level cluster instance. Called
// internally by Stateful and Stateless when App.Cluster is set.
// Safe to call multiple times with the same value (idempotent for
// the common case of multiple handlers sharing one App).
func SetCluster(c Cluster) {
	cluster = c
}

// registerTopic records a topic name in the global registry. Panics
// if the topic has already been registered. This catches configuration
// errors at startup rather than producing silent data corruption at
// runtime.
func registerTopic(topic string) {
	topicRegistry.mu.Lock()
	defer topicRegistry.mu.Unlock()
	if topicRegistry.topics[topic] {
		panic(fmt.Sprintf("tether: duplicate cluster topic %q", topic))
	}
	topicRegistry.topics[topic] = true
}

// unregisterTopic removes a topic from the global registry. Used in
// tests to allow re-registration between test cases.
func unregisterTopic(topic string) {
	topicRegistry.mu.Lock()
	defer topicRegistry.mu.Unlock()
	delete(topicRegistry.topics, topic)
}

// resetCluster clears the package-level cluster and topic registry.
// Only for use in tests.
func resetCluster() {
	cluster = nil
	topicRegistry.mu.Lock()
	topicRegistry.topics = make(map[string]bool)
	topicRegistry.mu.Unlock()
}

// generateNodeID creates a unique identifier for this process using
// crypto/rand. The ID is embedded in every cluster message so
// receiving nodes can filter out their own publications.
func generateNodeID() string {
	return rand.Text()
}

// busEnvelope wraps a Bus event for cluster transport. The event is
// CBOR-encoded as opaque bytes because the cluster layer does not
// know the generic type parameter.
type busEnvelope struct {
	NodeID   string `cbor:"n"`
	SenderID string `cbor:"s,omitempty"` // session ID for Emit (sender filtering)
	Data     []byte `cbor:"d"`
}

// valueEnvelope wraps a Value update for cluster transport.
type valueEnvelope struct {
	NodeID string `cbor:"n"`
	Data   []byte `cbor:"d"`
}

// marshalEnvelopeCBOR encodes a value using CBOR for cluster transport.
func marshalEnvelopeCBOR(v any) ([]byte, error) {
	return cbor.Marshal(v)
}

// unmarshalBusEnvelope decodes a busEnvelope from CBOR bytes.
func unmarshalBusEnvelope(data []byte) (busEnvelope, error) {
	var env busEnvelope
	err := cbor.Unmarshal(data, &env)
	return env, err
}

// unmarshalValueEnvelope decodes a valueEnvelope from CBOR bytes.
func unmarshalValueEnvelope(data []byte) (valueEnvelope, error) {
	var env valueEnvelope
	err := cbor.Unmarshal(data, &env)
	return env, err
}

// clusterPublish sends data to the cluster, logging any error at Warn
// level. Local operations always succeed regardless of cluster health.
func clusterPublish(topic string, data []byte) {
	if err := cluster.Publish(context.Background(), topic, data); err != nil {
		dev.Log().Warn("cluster publish failed", "topic", topic, "error", err)
	}
}
