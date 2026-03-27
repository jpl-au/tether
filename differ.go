package tether

import (
	"io"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
)

// engine is the internal interface for the session's diff strategy.
// Both [jit.Differ] and [jit.Memoiser] satisfy it. The session does
// not know which implementation it holds - the choice is made at
// session creation time based on [StatefulConfig.Memo].
type engine interface {
	Render(root node.Node, w ...io.Writer) []byte
	Diff(root node.Node) ([]jit.Patch, *jit.StructuralChange)
	Export() []byte
	Import(data []byte) error
	Clear()
}

// engine returns the diff strategy for this handler. When Memo is
// enabled, a Memoiser is created and seeded with the tree. When
// disabled, the pre-seeded Differ from the pending session is used
// directly. The Memoiser needs its own Render call to collect memo
// keys from the tree.
func (h *Handler[S]) engine(d *jit.Differ, state S) engine {
	if h.cfg.Memo {
		m := jit.NewMemoiser()
		tree := h.cfg.Render(state)
		m.Render(tree)
		return m
	}
	return d
}
