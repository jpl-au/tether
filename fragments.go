package tether

import (
	"bytes"
	"encoding/hex"
	"hash/fnv"

	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// Auto-fragments bring targeted updates to stateless pages without
// the developer declaring keys. A stateless server holds no previous
// render to diff against, so the client keeps the state instead: it
// remembers a content hash per Dynamic fragment (seeded by the
// initial GET, refreshed by every response) and echoes the map with
// each event. The server hashes its fresh render and sends back only
// the fragments whose content actually changed.
//
// Everything below hangs off a single render pass. The tree renders
// exactly once per request - decomposed the way the jit Differ
// renders - and every keyed region is captured as a byte extent of
// that output. Fragments, morphs and hashes are all slices of the
// same bytes the client receives, so a Func closure whose evaluations
// differ (unsorted map range, time-dependent content) cannot cause
// the client to store a hash for content it was never sent. An
// earlier implementation rendered the page and then walked the tree
// again to render fragments, which evaluated every closure twice and
// computed hashes from the second evaluation.

// extent records where a Dynamic-keyed region landed in the page
// render.
type extent struct {
	key    string
	start  int
	end    int
	nested bool // lies inside another keyed region
}

// renderPage renders the tree once, returning the page bytes and the
// extent of every keyed region. The buffer is deliberately not
// pooled: fragment and morph slices alias its backing array for the
// life of the response, and a pooled buffer would hand that array to
// the next render.
func renderPage(tree node.Node) ([]byte, []extent) {
	var buf bytes.Buffer
	var exts []extent
	renderExtents(tree, &buf, &exts, 0)
	return buf.Bytes(), exts
}

// renderExtents renders n into buf exactly once, decomposed the same
// way as the jit Differ: open tag, children, close tag. The generated
// element code guarantees this sequence is byte-identical to
// RenderBuilder. Containers without markup of their own (fragments,
// conditionals, function components) contribute their children,
// evaluating any closure once via Nodes. Nodes without children
// render via RenderBuilder.
func renderExtents(n node.Node, buf *bytes.Buffer, exts *[]extent, depth int) {
	key := ""
	if d, ok := n.(node.Dynamic); ok {
		key = d.DynamicKey()
	}
	idx := -1
	if key != "" {
		idx = len(*exts)
		*exts = append(*exts, extent{key: key, start: buf.Len(), nested: depth > 0})
		depth++
	}

	if el, ok := n.(node.Element); ok {
		el.RenderOpen(buf)
		for _, child := range n.Nodes() {
			if child != nil {
				renderExtents(child, buf, exts, depth)
			}
		}
		el.RenderClose(buf)
	} else if children := n.Nodes(); len(children) > 0 {
		for _, child := range children {
			if child != nil {
				renderExtents(child, buf, exts, depth)
			}
		}
	} else {
		n.RenderBuilder(buf)
	}

	if idx >= 0 {
		(*exts)[idx].end = buf.Len()
	}
}

// fragments returns the outermost keyed regions as slices of html.
// Nested keys are excluded so each byte of the page is owned by at
// most one fragment.
func fragments(html []byte, exts []extent) map[string][]byte {
	frags := make(map[string][]byte, len(exts))
	for _, e := range exts {
		if !e.nested {
			frags[e.key] = html[e.start:e.end]
		}
	}
	return frags
}

// morphsFor returns a [wire.Morph] per wanted key: the first
// occurrence in document order wins, and a wanted key inside an
// already-matched region is not returned separately - its bytes
// already travel with the enclosing morph. Keys not found in the
// tree are silently skipped; the caller logs them in dev mode.
func morphsFor(html []byte, exts []extent, keys []string) []wire.Morph {
	wanted := make(map[string]bool, len(keys))
	for _, k := range keys {
		wanted[k] = true
	}
	var morphs []wire.Morph
	var matched []extent
	for _, e := range exts {
		if !wanted[e.key] {
			continue
		}
		inside := false
		for _, m := range matched {
			if e.start >= m.start && e.end <= m.end {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		morphs = append(morphs, wire.Morph{Key: e.key, HTML: html[e.start:e.end]})
		matched = append(matched, e)
		delete(wanted, e.key)
	}
	return morphs
}

// warnUnstableFragments renders the tree a second time and compares
// fragments between two identical evaluations. A mismatch means a
// closure is nondeterministic (unsorted map range, time.Now, rand)
// and its hash will never survive the round-trip to the client: the
// fragment would be resent on every event, or worse, a change could
// be reported as unchanged against a hash the user never saw.
// Closure side effects fire twice as a consequence - that is the
// point, and only dev mode ever calls this.
func warnUnstableFragments(tree node.Node, frags map[string][]byte, path string) {
	html2, exts2 := renderPage(tree)
	frags2 := fragments(html2, exts2)
	for key, b := range frags {
		if b2, ok := frags2[key]; !ok || !bytes.Equal(b, b2) {
			dev.Warn("fragment renders differently across two identical evaluations",
				"key", key, "path", path,
				"hint", "nondeterministic closure - unsorted map range, time.Now or rand?")
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
