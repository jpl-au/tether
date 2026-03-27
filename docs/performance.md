# Performance

## Engine strategies

Tether provides multiple strategies for reducing render+diff cost:
coalescing, memoisation, targeted updates (Patch), and windowing.
See the [engine guide](engine.md) for a comprehensive reference on
how each works, when to use them, and how they compose.

## Generic helpers vs SetData

The generic helpers are ~47% slower than calling `SetData` directly. For performance-sensitive render paths, use `SetData`:

```go
button.Text("+").SetData("tether-click", "increment")
```

In practice the difference is ~250ns per element - negligible unless you're rendering thousands of event-bound elements per frame.

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

Both generic helpers and direct `SetData` paths benefit from PGO.

---

[← Back to documentation](../README.md#documentation)
