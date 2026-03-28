package tether

import (
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func TestEngineDefaultIsDiffer(t *testing.T) {
	d := jit.NewDiffer()
	h := &Handler[counterState]{
		cfg: StatefulConfig[counterState]{
			Render: func(s counterState) node.Node {
				return div.New(span.Text("x").Dynamic("x"))
			},
		},
	}

	e := h.engine(d, counterState{})
	if _, ok := e.(*jit.Differ); !ok {
		t.Errorf("expected *jit.Differ when Memoise is false, got %T", e)
	}
}

func TestEngineMemoiseIsMemoiser(t *testing.T) {
	d := jit.NewDiffer()
	h := &Handler[counterState]{
		cfg: StatefulConfig[counterState]{
			Memoise: true,
			Render: func(s counterState) node.Node {
				return div.New(span.Text("x").Dynamic("x"))
			},
		},
	}

	e := h.engine(d, counterState{})
	if _, ok := e.(*jit.Memoiser); !ok {
		t.Errorf("expected *jit.Memoiser when Memoise is true, got %T", e)
	}
}
