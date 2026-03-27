// Package window provides a server-side virtual scrolling helper for
// tether. Instead of rendering every item in a large list, only the
// visible portion is rendered. The tree stays O(page size) regardless
// of dataset size.
//
// The helper produces spacer divs above and below the visible rows to
// maintain the scroll container's height, so the browser scrollbar
// stays accurate. The developer handles scroll events to update the
// offset in state.
//
// Basic usage:
//
//	window.New(window.Config{
//	    Total:     len(s.Items),
//	    Offset:    s.ScrollOffset,
//	    PageSize:  30,
//	    RowHeight: 40,
//	    Row:       func(i int) node.Node { return renderRow(s.Items[i]) },
//	})
//
// Wire up the scroll event to update the offset:
//
//	bind.Apply(container, bind.OnViewport("scroll"), bind.Throttle(100*time.Millisecond))
//
// In Handle:
//
//	case "scroll":
//	    s.ScrollOffset = ev.Int("scrollTop") / rowHeight
package window

import (
	"strconv"

	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/node"
)

// Config describes the windowed view over a list. Total is the full
// dataset size, Offset is the first visible index, PageSize is how
// many items to render, and RowHeight is the uniform height in pixels
// of each row. Row is called for each visible index to produce the
// rendered node.
type Config struct {
	// Total is the number of items in the full dataset.
	Total int

	// Offset is the index of the first visible item. Updated by the
	// developer in response to scroll events.
	Offset int

	// PageSize is the number of items to render. Should be large
	// enough to fill the viewport plus a buffer for smooth scrolling.
	PageSize int

	// RowHeight is the uniform height of each row in pixels. Used to
	// calculate spacer heights so the scrollbar position is accurate.
	RowHeight int

	// Row renders a single item by index. Called for each visible
	// index from Offset to Offset+PageSize (clamped to Total).
	Row func(index int) node.Node
}

// New produces a windowed view: a top spacer, the visible rows, and a
// bottom spacer. The spacers are empty divs with a pixel height that
// accounts for the items above and below the visible window, keeping
// the scroll container's total height correct.
//
// The returned node is wrapped in a single Dynamic key ("window") so
// the differ treats the entire windowed region as one patchable unit.
// This avoids structural-change detection that would occur if each
// row had its own Dynamic key (scrolling would add/remove keys on
// every frame).
func New(cfg Config) node.Node {
	offset := clamp(cfg.Offset, 0, cfg.Total)
	end := clamp(offset+cfg.PageSize, 0, cfg.Total)

	topHeight := offset * cfg.RowHeight
	bottomHeight := (cfg.Total - end) * cfg.RowHeight

	rows := make([]node.Node, 0, end-offset+2)
	rows = append(rows, spacer(topHeight))
	for i := offset; i < end; i++ {
		rows = append(rows, cfg.Row(i))
	}
	rows = append(rows, spacer(bottomHeight))

	return div.New(rows...).Dynamic("window")
}

// spacer produces an empty div with a fixed pixel height. When height
// is zero, the div is still rendered (height: 0px) so the node count
// stays stable across frames.
func spacer(height int) node.Node {
	return div.New().Style("height:" + strconv.Itoa(height) + "px")
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
