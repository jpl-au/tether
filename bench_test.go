package tether

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/button"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/bind"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/wire"
)

// ---------------------------------------------------------------------------
// Generic tether.Click vs raw SetData
// ---------------------------------------------------------------------------

// BenchmarkBindClick measures Apply+OnClick + Render.
func BenchmarkBindClick(b *testing.B) {
	for i := 0; i < b.N; i++ {
		el := bind.Apply(button.Text("+"), bind.OnClick("increment"))
		_ = el.Render()
	}
}

// BenchmarkSetDataDirect measures raw SetData (no generic) + Render.
func BenchmarkSetDataDirect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		el := button.Text("+").SetData("tether-click", "increment")
		_ = el.Render()
	}
}

// BenchmarkBindClickRenderOnly isolates the render cost by pre-building
// the element, so only the Render() call is measured.
func BenchmarkBindClickRenderOnly(b *testing.B) {
	el := bind.Apply(button.Text("+"), bind.OnClick("increment"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = el.Render()
	}
}

// BenchmarkSetDataDirectRenderOnly is the SetData equivalent.
func BenchmarkSetDataDirectRenderOnly(b *testing.B) {
	el := button.Text("+").SetData("tether-click", "increment")
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
	patches := make([]wire.Patch, n)
	for i := range patches {
		key := fmt.Sprintf("key-%d", i)
		patches[i] = wire.Patch{
			Key:  key,
			HTML: fmt.Appendf(nil, `<span data-tether-key="%s">value %d</span>`, key, i),
		}
	}
	u := wire.Update{Patches: patches}
	enc := wire.JSONEncoder{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := enc.Encode(u)
		_ = data
	}
}

func BenchmarkEncodeUpdateMorph(b *testing.B) {
	html := []byte(`<div data-tether-root><span data-tether-key="count">42</span><span data-tether-key="name">Alice</span></div>`)
	u := wire.Update{
		Morphs: []wire.Morph{{Key: "", HTML: html}},
	}
	enc := wire.JSONEncoder{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := enc.Encode(u)
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

func benchHandle(_ Session, s benchState, ev Event) benchState {
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

func (d *discardTransport) Send(_ []byte) error { return nil }
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
		events[i] = Event{Type: event.Click, Action: "increment"}
	}

	dt := &discardTransport{events: events}
	differ := jit.NewDiffer()
	ctx, cancel := context.WithCancel(context.Background())

	sess := &StatefulSession[benchState]{
		id:        "bench",
		state:     benchState{Count: 0},
		render:    benchRender,
		handle:    benchHandle,
		differ:    differ,
		encoder:   wire.JSONEncoder{},
		transport: dt,
		events:    make(chan Event),
		cmds:      make(chan func(), defaultCmdBufferSize),
		fxCh:      make(chan func(*Effects), defaultCmdBufferSize),
		loopDone:  make(chan struct{}),
		destroyed: make(chan struct{}),
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
