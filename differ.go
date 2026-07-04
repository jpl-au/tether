package tether

import (
	"io"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
)

// engine is the internal interface for the session's diff strategy.
// Both [jit.Differ] and [jit.Memoiser] satisfy it. The session does
// not know which implementation it holds - the choice is made at
// session creation time based on [StatefulConfig.Memoise].
type engine interface {
	Render(root node.Node, w io.Writer)
	RenderBytes(root node.Node) []byte
	Diff(root node.Node) ([]jit.Patch, *jit.StructuralChange)
	DiffKey(key string, subtree node.Node) *jit.Patch
	Export() []byte
	Import(data []byte) error
	Clear()
}

// engine returns the diff strategy for this handler. When Memoise is
// enabled, a Memoiser is created and seeded with the tree. When
// disabled, the pre-seeded Differ from the pending session is used
// directly. The Memoiser needs its own Render call to collect memoisation
// keys from the tree.
//
// When seed is false (stale client reconnecting to a fresh server),
// the engine is left unseeded so the first Diff returns nil/nil,
// triggering a full morph in coalescedRender.
func (h *Handler[S]) engine(d *jit.Differ, state S, seed bool) engine {
	if h.cfg.Memoise {
		m := jit.NewMemoiser()
		if seed {
			tree := h.cfg.Render(state)
			m.Render(tree, io.Discard)
		}
		return m
	}
	return d
}
