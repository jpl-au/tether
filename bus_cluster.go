package tether

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/jpl-au/tether/dev"
)

// publish encodes the event and sends it to the cluster. No-op when
// the bus has no topic or no cluster is configured. Errors are logged
// at Warn level - local delivery always succeeds regardless.
func (b *Bus[E]) clusterPublish(event E, senderID string) {
	if b.topic == "" || cluster == nil {
		return
	}
	data, err := cbor.Marshal(event)
	if err != nil {
		dev.Log().Warn("cluster bus: failed to encode event",
			"topic", b.topic, "error", err)
		return
	}
	env := busEnvelope{
		NodeID:   nodeID,
		SenderID: senderID,
		Data:     data,
	}
	envData, err := marshalEnvelopeCBOR(env)
	if err != nil {
		dev.Log().Warn("cluster bus: failed to encode envelope",
			"topic", b.topic, "error", err)
		return
	}
	clusterPublish(fmt.Sprintf("tether:bus:%s", b.topic), envData)
}

// initCluster lazily subscribes to the cluster topic on first use.
// The subscription delivers remote events to local subscribers with
// self-filtering by node ID.
func (b *Bus[E]) initCluster() {
	if b.topic == "" || cluster == nil {
		return
	}
	b.clusterOnce.Do(func() {
		topic := fmt.Sprintf("tether:bus:%s", b.topic)
		b.clusterUnsub = cluster.Subscribe(topic, func(data []byte) {
			env, err := unmarshalBusEnvelope(data)
			if err != nil {
				dev.Log().Warn("cluster bus: failed to decode envelope",
					"topic", b.topic, "error", err)
				return
			}
			if env.NodeID == nodeID {
				return
			}
			var event E
			if err := cbor.Unmarshal(env.Data, &event); err != nil {
				dev.Log().Warn("cluster bus: failed to decode event",
					"topic", b.topic, "error", err)
				return
			}
			// Deliver to local subscribers. The sender ID from the
			// remote node is passed through so session-bound subscribers
			// on the originating node already filtered correctly. On
			// this node, no session has that ID so all local subscribers
			// receive the event.
			b.publish(event, env.SenderID)
		})
	})
}
