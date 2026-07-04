package tether

import (
	"encoding/hex"
	"hash/fnv"

	"github.com/jpl-au/fluent/node"
)

// Auto-fragments bring targeted updates to stateless pages without
// the developer declaring keys. A stateless server holds no previous
// render to diff against, so the client keeps the state instead: it
// remembers a content hash per Dynamic fragment (seeded by the
// initial GET, refreshed by every response) and echoes the map with
// each event. The server hashes its fresh render and sends back only
// the fragments whose content actually changed.

// collectFragments walks the node tree and renders every outermost
// Dynamic-keyed subtree, returning key → rendered HTML. The walk
// stops descending once a keyed node matches (mirroring
// extractMorphs), so nested keys belong to their enclosing fragment
// and each byte of the page is owned by at most one fragment.
func collectFragments(root node.Node) map[string][]byte {
	frags := make(map[string][]byte)
	walkFragments(root, frags)
	return frags
}

func walkFragments(n node.Node, frags map[string][]byte) {
	if d, ok := n.(node.Dynamic); ok {
		if key := d.DynamicKey(); key != "" {
			frags[key] = n.RenderBytes()
			return
		}
	}
	for _, child := range n.Nodes() {
		if child != nil {
			walkFragments(child, frags)
		}
	}
}

// fragmentHashes computes the content hash for each rendered
// fragment. FNV-1a is deliberate: the hash only needs to detect that
// a fragment's bytes differ between two renders - it guards
// bandwidth, not integrity - and FNV is fast, dependency-free, and
// deterministic across processes, so hashes seeded by one server
// instance remain valid against another.
func fragmentHashes(frags map[string][]byte) map[string]string {
	hashes := make(map[string]string, len(frags))
	for key, html := range frags {
		h := fnv.New64a()
		h.Write(html)
		hashes[key] = hex.EncodeToString(h.Sum(nil))
	}
	return hashes
}

// sameKeys reports whether the client's hash map covers exactly the
// keys of the fresh render. A mismatch means the page's fragment
// structure changed (keys added or removed), which targeted morphs
// cannot express - the caller falls back to a full root morph.
func sameKeys(client map[string]string, fresh map[string]string) bool {
	if len(client) != len(fresh) {
		return false
	}
	for key := range fresh {
		if _, ok := client[key]; !ok {
			return false
		}
	}
	return true
}
