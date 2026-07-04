package tether

import (
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/wire"
)

// extractMorphs walks the node tree and renders subtrees whose Dynamic
// key appears in the wanted set. Each matching subtree is returned as
// a keyed [wire.Morph] suitable for the client's applyMorph function.
//
// The walk stops descending into a node once its key matches, so
// nested keys inside a matched subtree are not returned separately.
// Keys in the wanted set that are not found in the tree are silently
// skipped - the caller should log missing keys in dev mode.
func extractMorphs(root node.Node, keys []string) []wire.Morph {
	wanted := make(map[string]bool, len(keys))
	for _, k := range keys {
		wanted[k] = true
	}
	var morphs []wire.Morph
	walkMorphKeys(root, wanted, &morphs)
	return morphs
}

// walkMorphKeys performs a depth-first walk of the node tree,
// collecting rendered HTML for nodes whose Dynamic key is in the
// wanted set. Found keys are removed from wanted so each key
// produces at most one morph.
func walkMorphKeys(n node.Node, wanted map[string]bool, morphs *[]wire.Morph) {
	if len(wanted) == 0 {
		return
	}
	if d, ok := n.(node.Dynamic); ok {
		key := d.DynamicKey()
		if key != "" && wanted[key] {
			*morphs = append(*morphs, wire.Morph{Key: key, HTML: n.RenderBytes()})
			delete(wanted, key)
			return
		}
	}
	for _, child := range n.Nodes() {
		if child != nil {
			walkMorphKeys(child, wanted, morphs)
		}
	}
}
