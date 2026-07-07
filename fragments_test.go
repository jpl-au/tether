package tether

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/li"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/html5/ul"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// TestRenderPageMatchesRenderBytes guards the decomposed-render
// contract renderExtents relies on: open tag, children, close tag
// must be byte-identical to RenderBuilder for the same tree.
func TestRenderPageMatchesRenderBytes(t *testing.T) {
	card := div.New(
		span.Text("title"),
		node.Func(func() node.Node {
			return ul.New(li.Text("one"), li.Text("two"))
		}),
		node.Funcs(func() []node.Node {
			return []node.Node{span.Text("a"), span.Text("b")}
		}),
		node.When(true, span.Text("shown")),
		node.When(false, span.Text("hidden")),
	).Class("card")
	card.Dynamic("card")
	inner := span.Text("nested")
	inner.Dynamic("nested")
	tree := div.New(card, div.New(inner)).ID("page")

	html, _ := renderPage(tree)
	if !bytes.Equal(html, tree.RenderBytes()) {
		t.Fatalf("renderPage output differs from RenderBytes:\n got %s\nwant %s",
			html, tree.RenderBytes())
	}
}

// TestRenderPageEvaluatesClosuresOnce pins the guarantee the rewrite
// exists for: one render pass, one evaluation, wherever the closure
// sits. The previous implementation evaluated every closure twice
// per request (page render plus fragment walk).
func TestRenderPageEvaluatesClosuresOnce(t *testing.T) {
	var inKeyed, outKeyed int
	card := div.New(node.Func(func() node.Node {
		inKeyed++
		return span.Text("inside")
	}))
	card.Dynamic("frag")
	tree := div.New(
		card,
		node.Func(func() node.Node {
			outKeyed++
			return span.Text("outside")
		}),
	)

	_, _ = renderPage(tree)

	if inKeyed != 1 {
		t.Errorf("closure inside keyed fragment evaluated %d times, want exactly 1", inKeyed)
	}
	if outKeyed != 1 {
		t.Errorf("closure outside keyed regions evaluated %d times, want exactly 1", outKeyed)
	}
}

// TestFragmentsOutermostOnly checks byte ownership: a nested key does
// not become its own fragment (its bytes belong to the enclosing
// fragment), but its extent is still recorded so morphsFor can reach
// it.
func TestFragmentsOutermostOnly(t *testing.T) {
	inner := span.Text("inner content")
	inner.Dynamic("inner")
	outer := div.New(inner)
	outer.Dynamic("outer")
	tree := div.New(outer)

	html, exts := renderPage(tree)
	frags := fragments(html, exts)

	if _, ok := frags["outer"]; !ok {
		t.Error("outermost key missing from fragments")
	}
	if _, ok := frags["inner"]; ok {
		t.Error("nested key must not be its own fragment")
	}
	if !bytes.Contains(frags["outer"], []byte("inner content")) {
		t.Error("outer fragment should contain the nested key's bytes")
	}

	if morphs := morphsFor(html, exts, []string{"inner"}); len(morphs) != 1 {
		t.Errorf("morphsFor should reach the nested key, got %d morphs", len(morphs))
	}
}

// TestMorphsForSemantics pins the behaviour inherited from the old
// extractMorphs walker: document order, at most one morph per key,
// a wanted key inside an already-matched region is skipped (its bytes
// travel with the enclosing morph), and missing keys are silent.
func TestMorphsForSemantics(t *testing.T) {
	child := span.Text("child")
	child.Dynamic("child")
	parent := div.New(child)
	parent.Dynamic("parent")
	sibling := span.Text("sibling")
	sibling.Dynamic("sibling")
	tree := div.New(parent, sibling)

	html, exts := renderPage(tree)

	// Parent and child both wanted: the child is inside the matched
	// parent, so only the parent morph travels.
	morphs := morphsFor(html, exts, []string{"parent", "child"})
	if len(morphs) != 1 || morphs[0].Key != "parent" {
		t.Fatalf("wanted parent+child: got %d morphs, first key %q; want the parent morph only",
			len(morphs), morphs[0].Key)
	}
	if !bytes.Contains(morphs[0].HTML, []byte("child")) {
		t.Error("parent morph should carry the child's bytes")
	}

	// Document order regardless of requested order.
	morphs = morphsFor(html, exts, []string{"sibling", "parent"})
	if len(morphs) != 2 || morphs[0].Key != "parent" || morphs[1].Key != "sibling" {
		t.Fatalf("expected document order [parent sibling], got %v", morphKeys(morphs))
	}

	// Missing keys are silently skipped.
	if morphs := morphsFor(html, exts, []string{"missing"}); len(morphs) != 0 {
		t.Errorf("missing key produced %d morphs, want 0", len(morphs))
	}
}

func morphKeys(morphs []wire.Morph) []string {
	keys := make([]string, len(morphs))
	for i, m := range morphs {
		keys[i] = m.Key
	}
	return keys
}

// TestStatelessGETHashesMatchServedFragments is the coherence test
// the rewrite exists for: with a closure that renders differently on
// every evaluation, the hash map seeded to the client must describe
// the fragment bytes actually present in the served page. The old
// two-pass pipeline hashed a second evaluation and failed this.
func TestStatelessGETHashesMatchServedFragments(t *testing.T) {
	evaluation := 0
	handler := Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render: func(s counterState) node.Node {
			frag := ul.New(node.Func(func() node.Node {
				evaluation++
				return li.Text(fmt.Sprintf("render-%d", evaluation))
			}))
			frag.Dynamic("frag")
			return div.New(frag)
		},
		Handle:        statelessHandleCounter,
		AutoFragments: true,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, "render-1") {
		t.Fatal("served page should contain the first evaluation's content")
	}

	// The fragment as served: the only <ul> in the page.
	start := strings.Index(body, "<ul")
	end := strings.Index(body, "</ul>") + len("</ul>")
	if start < 0 || end < len("</ul>") {
		t.Fatal("keyed fragment not found in served page")
	}
	served := body[start:end]

	// The hash map as seeded.
	islandStart := strings.Index(body, "<template data-tether-hashes>")
	islandEnd := strings.Index(body, "</template>")
	if islandStart < 0 || islandEnd < 0 {
		t.Fatal("hash island not found in served page")
	}
	var hashes map[string]string
	payload := body[islandStart+len("<template data-tether-hashes>") : islandEnd]
	if err := json.Unmarshal([]byte(payload), &hashes); err != nil {
		t.Fatalf("hash island is not valid JSON: %v", err)
	}

	want := fragmentHashes(map[string][]byte{"frag": []byte(served)})["frag"]
	if hashes["frag"] != want {
		t.Errorf("seeded hash %q does not describe the served fragment (hash %q) - client would store a hash for bytes it never received",
			hashes["frag"], want)
	}
}

// TestWarnUnstableFragments checks the dev-mode detector: two
// identical evaluations that produce different bytes warn with the
// fragment's key; deterministic fragments stay silent.
func TestWarnUnstableFragments(t *testing.T) {
	t.Cleanup(dev.Reset)
	dev.Enable()

	var logBuf bytes.Buffer
	dev.SetLogger(slog.New(slog.NewTextHandler(&logBuf, nil)))

	evaluation := 0
	unstable := ul.New(node.Func(func() node.Node {
		evaluation++
		return li.Text(fmt.Sprintf("render-%d", evaluation))
	}))
	unstable.Dynamic("unstable")
	stable := span.Text("same every time")
	stable.Dynamic("stable")
	tree := div.New(unstable, stable)

	html, exts := renderPage(tree)
	warnUnstableFragments(tree, fragments(html, exts), "/app")

	logged := logBuf.String()
	if !strings.Contains(logged, "renders differently") || !strings.Contains(logged, "unstable") {
		t.Errorf("expected instability warning for key 'unstable', got: %s", logged)
	}
	if strings.Contains(logged, `key=stable`) {
		t.Errorf("deterministic fragment must not warn, got: %s", logged)
	}
}
