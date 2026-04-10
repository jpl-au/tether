package tether

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/jpl-au/tether/dev"
)

// clusterPublish encodes the value and sends it to the cluster. No-op
// when the Value has no topic or no cluster is configured.
func (v *Value[V]) clusterPublish(val V) {
	if v.topic == "" || getCluster() == nil {
		return
	}
	data, err := cbor.Marshal(val)
	if err != nil {
		dev.Log().Warn("cluster value: failed to encode value",
			"topic", v.topic, "error", err)
		return
	}
	env := valueEnvelope{
		NodeID: nodeID,
		Data:   data,
	}
	envData, err := marshalEnvelopeCBOR(env)
	if err != nil {
		dev.Log().Warn("cluster value: failed to encode envelope",
			"topic", v.topic, "error", err)
		return
	}
	clusterPublish(fmt.Sprintf("tether:value:%s", v.topic), envData)
}

// initCluster lazily subscribes to the cluster topic on first use.
// The subscription calls storeLocal to update the value from remote
// changes without re-publishing to the cluster.
func (v *Value[V]) initCluster() {
	if v.topic == "" || getCluster() == nil {
		return
	}
	v.clusterOnce.Do(func() {
		topic := fmt.Sprintf("tether:value:%s", v.topic)
		v.clusterUnsub = getCluster().Subscribe(topic, func(data []byte) {
			env, err := unmarshalValueEnvelope(data)
			if err != nil {
				dev.Log().Warn("cluster value: failed to decode envelope",
					"topic", v.topic, "error", err)
				return
			}
			if env.NodeID == nodeID {
				return
			}
			var val V
			if err := cbor.Unmarshal(env.Data, &val); err != nil {
				dev.Log().Warn("cluster value: failed to decode value",
					"topic", v.topic, "error", err)
				return
			}
			v.storeLocal(val)
		})
	})
}
