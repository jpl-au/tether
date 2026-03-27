# Performance

## Generic helpers vs SetData

The generic helpers are ~47% slower than calling `SetData` directly. For performance-sensitive render paths, use `SetData`:

```go
button.Text("+").SetData("tether-click", "increment")
```

In practice the difference is ~250ns per element - negligible unless you're rendering thousands of event-bound elements per frame.

## Update coalescing

When multiple Updates arrive in quick succession (broadcasts, Value
changes, watcher callbacks), the command loop drains all pending
commands before rendering. Each mutation runs, but only one
render-diff-send cycle executes for the batch. This is automatic -
no configuration needed.

Under broadcast load, this reduces redundant renders from O(N) to
O(1) per loop iteration. Under normal interactive load (one event
at a time), behaviour is identical - there is nothing to coalesce.

## Memoisation

For pages with expensive render functions, enable memoisation to skip
unchanged subtrees entirely. Set `Memo: true` on `StatefulConfig` and
wrap each Dynamic region's content in `node.Memo`:

```go
tether.Stateful(app, tether.StatefulConfig[State]{
    Memo: true,
    Render: func(s State) node.Node {
        return div.New(
            div.New(
                node.Memo(s.HeaderVersion, func() node.Node {
                    return renderHeader(s)
                }),
            ).Dynamic("header"),
            div.New(
                node.Memo(s.ItemsVersion, func() node.Node {
                    return renderTable(s.Items)
                }),
            ).Dynamic("items"),
        )
    },
    // ...
})
```

When a memo key matches the previous render, the closure never runs
and no HTML is rendered for that region. For a page with 50 Dynamic
regions where only one changed, the Memoiser is up to 40x faster
than the standard Differ.

The memo key can be any type - strings, ints, bools, and other
common types are converted efficiently with no reflection. Use a
version counter (`s.ItemsVersion++` when items change) for the
cheapest comparison.

**Diff vs Memo**: Diff is the default and requires zero developer
effort. Memo is opt-in for expensive subtrees. Use one or the other
per handler, not both. Dynamic regions without a `node.Memo` child
are always re-rendered when using the Memoiser.

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
