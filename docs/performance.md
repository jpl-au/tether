# Performance

## Engine strategies

Tether provides multiple strategies for reducing render+diff cost:
coalescing, memoisation, targeted updates (Patch), and windowing.
See the [engine guide](engine.md) for a comprehensive reference on
how each works, when to use them, and how they compose.

## Shared fragments across sessions

Memoisation caches a region's render *within* one session. When the same
region renders identically for many sessions - a shared header, a
navigation bar, a live scoreboard broadcast to a room - `jit.Shared`
extends that cache *across* sessions: the render runs at most once per
key for the whole process, and every other session is served those
bytes.

```go
// Requires StatefulConfig.Memoise: true
jit.Shared("leaderboard:"+s.BoardVersion, func() node.Node {
    return renderBoard(s.Board)   // runs once per version, not per session
})
```

Without it, a broadcast to N sessions re-renders the shared region N
times (once in each session's diff). With it, the first session renders
and the other N-1 reuse the bytes from a process-global cache. The
saving grows with the size of the room.

The key must be **globally unique** and must **fully determine the
rendered bytes** - namespace it and derive it from the content
(`"nav:v3"`, `"board:"+hash`), never from per-session state. Two
sessions with the same key are served the same bytes, so a key that
varies per user would serve one user's render to another. Plain
`jit.Memoise` is safe with a bare counter because its key is only
compared within one session; `jit.Shared` is compared across the whole
process, hence the stricter contract.

The cache is bounded (two generations, 2048 entries each by default;
tune with `tether.SetSharedCacheSize`). The [`SharedCacheReuse`
diagnostic](operations.md) reports per-render hits and misses - an
all-miss pattern across sessions means the keys are not aligning, which
usually means a key is derived from per-session state.

## bind.Apply vs SetData

`bind.Apply` and the option helpers cost ~25-30% more than calling `SetData` directly. For performance-sensitive render paths, use `SetData`:

```go
button.Text("+").SetData("tether-click", "increment")
```

In practice the difference is well under 100ns per element - negligible unless you're rendering thousands of event-bound elements per frame. Measure on your own hardware with `go test -bench 'BenchmarkBindClick|BenchmarkSetDataDirect'`; the ratio is stable but the absolute figures are not.

## Windowing

For large lists, render only the visible portion using the `window`
package. See the [windowing guide](windowing.md) for details.

```go
import "github.com/jpl-au/tether/window"

window.New(window.Config{
    Total:     len(s.Items),
    Offset:    s.ScrollOffset,
    PageSize:  30,
    RowHeight: 40,
    Row:       func(i int) node.Node { return renderRow(s.Items[i]) },
})
```

This keeps the tree at O(viewport) regardless of dataset size. A
10,000-item list with 30 visible rows is ~99% cheaper to render and
diff than rendering all 10,000 rows.

## Profile-Guided Optimisation (PGO)

Applications using tether benefit from [Profile-Guided Optimisation](https://go.dev/doc/pgo) (Go 1.21+). Expect **10-20% speed improvements** with no code changes.

1. Collect a CPU profile under realistic load:
   ```bash
   curl -o default.pgo http://localhost:8080/debug/pprof/profile?seconds=30
   ```
2. Place `default.pgo` in your main package directory
3. `go build` - PGO is applied automatically

Both `bind.Apply` and direct `SetData` paths benefit from PGO.

---

[← Back to documentation](../README.md#documentation)
