package poly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/button"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// ---------------------------------------------------------------------------
// Generic poly.Click vs raw SetData
// ---------------------------------------------------------------------------

// BenchmarkBindClick measures Click (generic wrapper) + Render.
func BenchmarkBindClick(b *testing.B) {
	for i := 0; i < b.N; i++ {
		el := Click(button.Text("+"), "increment")
		_ = el.Render()
	}
}

// BenchmarkSetDataDirect measures raw SetData (no generic) + Render.
func BenchmarkSetDataDirect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		el := button.Text("+").SetData("poly-click", "increment")
		_ = el.Render()
	}
}

// BenchmarkBindClickRenderOnly isolates the render cost by pre-building
// the element, so only the Render() call is measured.
func BenchmarkBindClickRenderOnly(b *testing.B) {
	el := Click(button.Text("+"), "increment")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = el.Render()
	}
}

// BenchmarkSetDataDirectRenderOnly is the SetData equivalent.
func BenchmarkSetDataDirectRenderOnly(b *testing.B) {
	el := button.Text("+").SetData("poly-click", "increment")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = el.Render()
	}
}

// ---------------------------------------------------------------------------
// Protocol encoding
// ---------------------------------------------------------------------------

func BenchmarkEncodeUpdatePatches1(b *testing.B) {
	benchEncodeUpdatePatches(b, 1)
}

func BenchmarkEncodeUpdatePatches10(b *testing.B) {
	benchEncodeUpdatePatches(b, 10)
}

func BenchmarkEncodeUpdatePatches100(b *testing.B) {
	benchEncodeUpdatePatches(b, 100)
}

func benchEncodeUpdatePatches(b *testing.B, n int) {
	patches := make([]jit.Patch, n)
	for i := range patches {
		key := fmt.Sprintf("key-%d", i)
		patches[i] = jit.Patch{
			Key:  key,
			HTML: fmt.Appendf(nil, `<span data-poly-key="%s">value %d</span>`, key, i),
		}
	}
	update := Update{Patches: patches}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := EncodeUpdate(update)
		data, _ := json.Marshal(msg)
		_ = data
	}
}

func BenchmarkEncodeUpdateMorph(b *testing.B) {
	html := []byte(`<div data-poly-root><span data-poly-key="count">42</span><span data-poly-key="name">Alice</span></div>`)
	update := Update{
		Morphs: []Morph{{Key: "", HTML: html}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := EncodeUpdate(update)
		data, _ := json.Marshal(msg)
		_ = data
	}
}

// ---------------------------------------------------------------------------
// Full event cycle (handle → render → diff → send)
// ---------------------------------------------------------------------------

type benchState struct {
	Count int
}

func benchRender(s benchState) node.Node {
	return div.New(
		span.Textf("Count: %d", s.Count).Dynamic("count"),
	)
}

func benchHandle(_ *Session[benchState], s benchState, ev Event) benchState {
	if ev.Action == "increment" {
		s.Count++
	}
	return s
}

// discardTransport satisfies Transport but discards all output.
type discardTransport struct {
	mu     sync.Mutex
	events []Event
}

func (d *discardTransport) SendUpdate(_ Update) error { return nil }
func (d *discardTransport) ReceiveEvent() (Event, error) {
	d.mu.Lock()
	if len(d.events) == 0 {
		d.mu.Unlock()
		return Event{}, io.EOF
	}
	ev := d.events[0]
	d.events = d.events[1:]
	d.mu.Unlock()
	return ev, nil
}
func (d *discardTransport) Close() error { return nil }

// BenchmarkEventCycle measures the full path per event: receive → handle →
// render → diff → send patches.
func BenchmarkEventCycle(b *testing.B) {
	events := make([]Event, b.N)
	for i := range events {
		events[i] = Event{Type: "click", Action: "increment"}
	}

	dt := &discardTransport{events: events}
	differ := jit.NewDiffer()
	ctx, cancel := context.WithCancel(context.Background())

	sess := &Session[benchState]{
		id:        "bench",
		state:     benchState{Count: 0},
		render:    benchRender,
		handle:    benchHandle,
		differ:    differ,
		transport: dt,
		events:    make(chan Event),
		cmds:      make(chan func(), cmdBufferSize),
		ctx:       ctx,
		stop:      cancel,
	}

	tree := benchRender(benchState{Count: 0})
	differ.Render(tree)

	b.ResetTimer()
	go sess.readTransport(sess.events)
	sess.run()
}

// ---------------------------------------------------------------------------
// Render + diff scaling
// ---------------------------------------------------------------------------

func BenchmarkDiffScale10(b *testing.B) {
	benchDiffScale(b, 10)
}

func BenchmarkDiffScale100(b *testing.B) {
	benchDiffScale(b, 100)
}

func BenchmarkDiffScale1000(b *testing.B) {
	benchDiffScale(b, 1000)
}

func benchDiffScale(b *testing.B, n int) {
	render := func(count int) node.Node {
		children := make([]node.Node, n)
		for i := range children {
			children[i] = span.Textf("item-%d-%d", i, count).
				Dynamic(fmt.Sprintf("k%d", i))
		}
		return div.New(children...)
	}

	differ := jit.NewDiffer()
	differ.Render(render(0))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree := render(i + 1)
		patches, change := differ.Diff(tree)
		if change != nil {
			differ.Render(tree)
		}
		_ = patches
	}
}
