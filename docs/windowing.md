# Windowing

For large lists, render only the visible portion instead of the
entire dataset. The `window` package produces spacer divs above and
below the visible rows to maintain scroll height, keeping the tree
at O(viewport) regardless of dataset size.

## Basic usage

```go
import "github.com/jpl-au/tether/window"

func render(s State) node.Node {
    return div.New(
        h1.Text("Items"),
        window.New(window.Config{
            Total:     len(s.Items),
            Offset:    s.ScrollOffset,
            PageSize:  30,
            RowHeight: 40,
            Row: func(i int) node.Node {
                return renderRow(s.Items[i])
            },
        }),
    )
}
```

## Handling scroll events

Wire up a viewport event on the scroll container to update the
offset in state:

```go
container := div.New(
    window.New(cfg),
).Style("overflow-y:auto; height:600px")

bind.Apply(container,
    bind.OnViewport("scroll"),
    bind.Throttle(100*time.Millisecond),
)
```

In Handle, convert the scroll position to an item offset:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    if ev.Action == "scroll" {
        scrollTop, _ := ev.Int("scrollTop")
        s.ScrollOffset = scrollTop / rowHeight
    }
    return s
},
```

## How it works

`window.New` produces a div with three children:

1. **Top spacer** - empty div with height = `offset * rowHeight`
2. **Visible rows** - rendered by the `Row` callback
3. **Bottom spacer** - empty div with height = `(total - offset - pageSize) * rowHeight`

The spacers keep the scroll container's total height correct so the
browser scrollbar position is accurate. The user scrolls as if the
full list were rendered.

## Config

| Field | Type | Description |
|-------|------|-------------|
| `Total` | `int` | Number of items in the full dataset |
| `Offset` | `int` | Index of the first visible item (from scroll events) |
| `PageSize` | `int` | How many items to render (viewport + buffer) |
| `RowHeight` | `int` | Uniform row height in pixels |
| `Row` | `func(int) node.Node` | Renders a single item by index |

## Dynamic key

The windowed container uses a single Dynamic key (`"window"`) so the
differ treats the entire region as one patchable unit. This is
deliberate - using a separate Dynamic key per row would cause
structural changes on every scroll (keys added and removed as rows
enter and leave the viewport), forcing a full morph instead of
targeted patches.

If you need per-row Dynamic keys within the window (e.g. for
targeted updates via a future `Patch` API), use stable slot-based
keys (`slot-0` through `slot-N`) rather than item-identity keys.
The key set stays constant across scrolls; only the content changes.

## Page size guidance

The initial render must fill the viewport before the first scroll
event arrives (the server does not know the viewport height until
then). Set `PageSize` generously - 50 rows is a safe default for
most layouts. The overhead of rendering 50 vs 30 rows is negligible
compared to rendering 10,000.

## Uniform row heights

The current implementation assumes all rows have the same pixel
height. Variable-height rows require a height map or estimation,
which is significantly more complex. For variable-height content,
consider server-side pagination instead of virtualisation.

## Accessibility

Screen readers and find-in-page only see the rendered rows. This is
a known tradeoff with virtualisation. For fully accessible
alternatives, use server-side pagination with page controls.

---

[← Back to documentation](../README.md#documentation)
