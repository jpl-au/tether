package tether

import (
	"context"
	"io"
	"strconv"
	"sync"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// sharedRegion builds a tree whose one Dynamic region is a node.Shared
// keyed by version, matching how a broadcast page would key a header or
// leaderboard by its content version.
func sharedRegion(version int) node.Node {
	return div.New(
		div.New(
			jit.Shared("nav:v"+strconv.Itoa(version), func() node.Node {
				return span.Text("nav")
			}),
		).Dynamic("nav"),
	)
}

// collectDiagnostics wires a fresh diagnostics bus to a session and
// returns a function that snapshots what has been emitted so far.
func collectDiagnostics(t *testing.T, s *StatefulSession[counterState]) func() []Diagnostic {
	t.Helper()
	bus := NewBus[Diagnostic]()
	s.diagnostics = bus
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var mu sync.Mutex
	var got []Diagnostic
	bus.Subscribe(ctx, func(d Diagnostic) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	})
	return func() []Diagnostic {
		mu.Lock()
		defer mu.Unlock()
		return append([]Diagnostic(nil), got...)
	}
}

// TestSharedCacheReuseDiagnostic drives two sessions with independent
// Memoiser engines but the same shared key through the tether stats
// path, and asserts the SharedCacheReuse diagnostic reports a miss for
// the session that rendered and a hit for the one that reused its bytes.
func TestSharedCacheReuseDiagnostic(t *testing.T) {
	jit.ResetSharedCache()

	newSharedSession := func(id string) *StatefulSession[counterState] {
		eng := jit.NewMemoiser()
		eng.Render(sharedRegion(1), io.Discard)
		return &StatefulSession[counterState]{id: id, engine: eng}
	}

	a := newSharedSession("A")
	snapA := collectDiagnostics(t, a)
	b := newSharedSession("B")
	snapB := collectDiagnostics(t, b)

	// Both regions advance to v2. Session A diffs first (renders and
	// populates the cache); session B diffs second (reuses A's bytes).
	a.engine.Diff(sharedRegion(2))
	a.checkMemoiseStats()
	b.engine.Diff(sharedRegion(2))
	b.checkMemoiseStats()

	wantDetail := func(snap func() []Diagnostic, want string) {
		t.Helper()
		for _, d := range snap() {
			if d.Kind == SharedCacheReuse {
				if d.Detail != want {
					t.Errorf("SharedCacheReuse detail = %q, want %q", d.Detail, want)
				}
				return
			}
		}
		t.Errorf("expected a SharedCacheReuse diagnostic, got none")
	}

	wantDetail(snapA, "0 hits, 1 misses")
	wantDetail(snapB, "1 hits, 0 misses")
}

// TestSharedCacheReuseDiagnosticSilentWithoutShared confirms the
// diagnostic does not fire for a plain memoised render that uses no
// node.Shared regions.
func TestSharedCacheReuseDiagnosticSilentWithoutShared(t *testing.T) {
	jit.ResetSharedCache()

	plain := func(v int) node.Node {
		return div.New(
			div.New(
				jit.Memoise("v"+strconv.Itoa(v), func() node.Node { return span.Text("x") }),
			).Dynamic("region"),
		)
	}
	eng := jit.NewMemoiser()
	eng.Render(plain(1), io.Discard)
	s := &StatefulSession[counterState]{id: "S", engine: eng}
	snap := collectDiagnostics(t, s)

	eng.Diff(plain(2))
	s.checkMemoiseStats()

	for _, d := range snap() {
		if d.Kind == SharedCacheReuse {
			t.Errorf("SharedCacheReuse should not fire without node.Shared, got %+v", d)
		}
	}
}
