package window

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func row(i int) node.Node {
	return span.Text("row-" + strconv.Itoa(i))
}

func TestNewRendersVisibleRows(t *testing.T) {
	n := New(Config{
		Total:     100,
		Offset:    10,
		PageSize:  5,
		RowHeight: 40,
		Row:       row,
	})
	html := string(n.RenderBytes())

	for i := 10; i < 15; i++ {
		want := "row-" + strconv.Itoa(i)
		if !strings.Contains(html, want) {
			t.Errorf("expected %q in output", want)
		}
	}
	// Rows outside the window should not appear.
	if strings.Contains(html, "row-9") {
		t.Error("row-9 should not be rendered")
	}
	if strings.Contains(html, "row-15") {
		t.Error("row-15 should not be rendered")
	}
}

func TestNewTopSpacer(t *testing.T) {
	n := New(Config{
		Total:     100,
		Offset:    20,
		PageSize:  5,
		RowHeight: 40,
		Row:       row,
	})
	html := string(n.RenderBytes())

	// Top spacer: 20 rows * 40px = 800px.
	if !strings.Contains(html, "height:800px") {
		t.Error("expected top spacer of 800px")
	}
}

func TestNewBottomSpacer(t *testing.T) {
	n := New(Config{
		Total:     100,
		Offset:    0,
		PageSize:  10,
		RowHeight: 40,
		Row:       row,
	})
	html := string(n.RenderBytes())

	// Bottom spacer: (100-10) * 40 = 3600px.
	if !strings.Contains(html, "height:3600px") {
		t.Error("expected bottom spacer of 3600px")
	}
}

func TestNewClampsOffset(t *testing.T) {
	// Offset beyond total should not panic.
	n := New(Config{
		Total:     5,
		Offset:    100,
		PageSize:  10,
		RowHeight: 40,
		Row:       row,
	})
	html := string(n.RenderBytes())

	// All items are above the window; no rows rendered, bottom spacer is 0.
	if strings.Contains(html, "row-") {
		t.Error("no rows should be rendered when offset exceeds total")
	}
}

func TestNewClampsNegativeOffset(t *testing.T) {
	n := New(Config{
		Total:     10,
		Offset:    -5,
		PageSize:  3,
		RowHeight: 40,
		Row:       row,
	})
	html := string(n.RenderBytes())

	// Should start from 0.
	if !strings.Contains(html, "row-0") {
		t.Error("expected row-0 when offset is negative")
	}
	// Top spacer should be 0.
	if !strings.Contains(html, "height:0px") {
		t.Error("expected 0px top spacer for negative offset")
	}
}

func TestNewEmptyDataset(t *testing.T) {
	n := New(Config{
		Total:     0,
		Offset:    0,
		PageSize:  10,
		RowHeight: 40,
		Row:       row,
	})
	html := string(n.RenderBytes())

	if strings.Contains(html, "row-") {
		t.Error("no rows should be rendered for empty dataset")
	}
}

func TestNewHasDynamicKey(t *testing.T) {
	n := New(Config{
		Total:     10,
		Offset:    0,
		PageSize:  5,
		RowHeight: 40,
		Row:       row,
	})
	html := string(n.RenderBytes())

	if !strings.Contains(html, `data-tether-key="window"`) {
		t.Error("expected Dynamic key on windowed container")
	}
}
