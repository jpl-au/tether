// Package tetheredis implements the tether Cluster interface using
// Redis Pub/Sub. It provides cross-node communication for tether's
// Bus and Value types by publishing and subscribing to Redis channels.
//
// Create a Cluster with New and pass it to tether.App{Cluster: ...}.
// The package does not import tether directly - it implements a
// matching interface shape to avoid circular dependencies.
package tetheredis

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// Cluster implements the tether Cluster interface using Redis Pub/Sub.
type Cluster struct {
	rdb *redis.Client
}

// New creates a Cluster backed by the given Redis client.
func New(rdb *redis.Client) *Cluster {
	return &Cluster{rdb: rdb}
}

// Publish sends data to all subscribers of the given topic via Redis
// PUBLISH.
func (c *Cluster) Publish(ctx context.Context, topic string, data []byte) error {
	return c.rdb.Publish(ctx, topic, data).Err()
}

// Subscribe registers a callback for messages on the given topic.
// It starts a background goroutine that reads from a Redis Pub/Sub
// subscription and invokes fn for each message. The returned function
// closes the subscription and stops the goroutine.
func (c *Cluster) Subscribe(topic string, fn func(data []byte)) func() {
	sub := c.rdb.Subscribe(context.Background(), topic)

	// Wait for the subscription to be confirmed so the caller can
	// publish immediately after subscribing without a race.
	_, err := sub.Receive(context.Background())
	if err != nil {
		panic("tetheredis: subscribe failed: " + err.Error())
	}

	ch := sub.Channel()

	go func() {
		for msg := range ch {
			fn([]byte(msg.Payload))
		}
	}()

	return func() {
		if err := sub.Close(); err != nil {
			slog.Warn("tetheredis: unsubscribe failed", "topic", topic, "error", err)
		}
	}
}
