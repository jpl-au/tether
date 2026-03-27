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
unchanged subtrees entirely. Set `Memo: true` on `StatefulConfig`
and wrap expensive Dynamic regions in `node.Memo` with a
`tether.Versioned` key.

### How it works

When a memo key matches the previous render, the closure never runs
and no HTML is rendered for that region. For a page with 50 Dynamic
regions where only one changed, the Memoiser is up to 40x faster
than the standard Differ.

Cheap regions that change frequently (counters, input fields) can
use plain Dynamic keys without Memo. They re-render every cycle as
usual. Use Memo only where the render function is expensive enough
to justify the version tracking.

### Versioned helper

`tether.Versioned[T]` bundles data with an automatic version counter.
The version increments on every `With` call, so the memo key always
tracks data changes without manual bookkeeping.

```go
type State struct {
    Items  tether.Versioned[[]Item]  // expensive table - memoised
    Search string                     // cheap input - plain Dynamic
    Count  int                        // cheap counter - plain Dynamic
}
```

Update data via `With` (version increments automatically). Read
data directly via `Val`:

```go
// Handle
func handle(sess tether.Session, s State, ev tether.Event) State {
    switch ev.Action {
    case "add-item":
        s.Items = s.Items.With(append(s.Items.Val, newItem(ev)))
    case "search":
        s.Search = ev.Value()
    case "increment":
        s.Count++
        // Items.Version() unchanged - table render is skipped
    }
    return s
}
```

In the render function, wrap expensive regions in `node.Memo` using
the version as the key. Cheap regions use plain Dynamic keys:

```go
// Render
func render(s State) node.Node {
    return div.New(
        // Expensive table - memoised. Only re-renders when
        // Items.Version() changes (i.e. when With was called).
        div.New(
            node.Memo(s.Items.Version(), func() node.Node {
                return renderTable(s.Items.Val)
            }),
        ).Dynamic("items"),

        // Cheap regions - plain Dynamic. Always re-render.
        input.New().Value(s.Search).Dynamic("search"),
        span.Text(strconv.Itoa(s.Count)).Dynamic("count"),
    )
}
```

Enable memoisation on the handler:

```go
tether.Stateful(app, tether.StatefulConfig[State]{
    Memo:         true,
    InitialState: func(r *http.Request) State {
        return State{Items: tether.NewVersioned(loadItems())}
    },
    Render: render,
    Handle: handle,
    // ...
})
```

### Diff vs Memo

Diff is the default. It requires zero developer effort - write a
render function and it works. Every Dynamic region is rendered and
compared on every cycle.

Memo is opt-in for handlers with expensive render functions. It
requires `Versioned` fields (or comparable keys) for memoised
regions. Dynamic regions without a `node.Memo` child are always
re-rendered, so cheap regions work normally alongside memoised ones.

Use one strategy per handler, not both. The `Memo` config field
selects which engine the session uses.

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
