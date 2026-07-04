package tether

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/wire"
)

func TestPatchUpdatesTargetedKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()

		// Patch the "count" key with new content.
		sess.Patch("count", func(s counterState) (counterState, node.Node) {
			s.Count = 42
			return s, span.Text("42").Dynamic("count")
		})
		synctest.Wait()

		// State should be updated.
		if sess.State().Count != 42 {
			t.Errorf("expected Count=42, got %d", sess.State().Count)
		}

		// Transport should have received a patch.
		ct.mu.Lock()
		sent := ct.sent
		ct.mu.Unlock()

		if len(sent) == 0 {
			t.Fatal("expected at least one message sent")
		}

		// Find a message with a patch for "count" in the JSON.
		found := false
		for _, msg := range sent {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(msg, &raw); err != nil {
				continue
			}
			if patches, ok := raw["patches"]; ok {
				if strings.Contains(string(patches), `"count"`) {
					found = true
				}
			}
		}
		if !found {
			t.Error("expected a patch for key 'count' in sent messages")
		}

		sess.stop()
		synctest.Wait()
	})
}

func TestPatchNoChangeDoesNotSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()

		// Wait for initial render to settle.
		synctest.Wait()

		ct.mu.Lock()
		beforeCount := len(ct.sent)
		ct.mu.Unlock()

		// Patch with identical content for "count" - no message should be sent.
		sess.Patch("count", func(s counterState) (counterState, node.Node) {
			return s, span.Textf("Count: %d", s.Count).Dynamic("count")
		})
		synctest.Wait()

		ct.mu.Lock()
		afterCount := len(ct.sent)
		ct.mu.Unlock()

		// The patch produces the same content as the current snapshot,
		// so DiffKey returns nil and no message is sent.
		if afterCount != beforeCount {
			t.Errorf("expected no new messages for unchanged patch, got %d new", afterCount-beforeCount)
		}

		sess.stop()
		synctest.Wait()
	})
}

func TestPatchBreaksSubsequentFullDiff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()

		// 1. Patch the "count" key to 1.
		sess.Patch("count", func(s counterState) (counterState, node.Node) {
			s.Count = 1
			return s, span.Text("Count: 1").Dynamic("count")
		})
		synctest.Wait()

		// 2. Clear sent messages.
		ct.mu.Lock()
		ct.sent = nil
		ct.mu.Unlock()

		// 3. Send an "increment" event. The handler will increment 1 -> 2.
		// The full render will produce "Count: 2".
		ct.ch <- Event{Type: "click", Action: "increment"}
		synctest.Wait()

		// 4. Verify that we received a patch for "Count: 2".
		ct.mu.Lock()
		sent := ct.sent
		ct.mu.Unlock()

		if len(sent) == 0 {
			t.Fatal("expected a message for the full render after increment, but got none")
		}

		found := false
		for _, msg := range sent {
			if strings.Contains(string(msg), "Count: 2") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a patch for 'Count: 2' after full render, but got: %v", sent)
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestPatchThroughDynamicParent reproduces the real patch demo bug.
// The page uses Dynamic("page") as an outer wrapper that contains
// many counter rows, each with their own Dynamic("counter-N") key.
// collectSnapshots stops at the page wrapper (it's Dynamic with a
// real key), so counter rows are NOT tracked in the initial order.
//
// sess.Patch("counter-5", ...) adds counter-5 to d.snapshots but
// NOT to d.order. A subsequent full Diff walks the tree, stops at
// "page", and only diffs page against its snapshot. Since the reset
// render produces the same page HTML as the initial render (all
// zeros), no patches are produced - even though counter-5 clearly
// has a stale snapshot showing "1".
//
// Expected: the framework should produce patches that reset the
// client's view of counter-5 back to zero.
// Actual: zero patches. Client sees "1", server sees "0".
func TestPatchThroughDynamicParent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 5

		type manyState struct {
			Counters [n]int
		}

		renderRow := func(i, value int) node.Node {
			return span.Textf("Counter %d: %d", i, value).
				Dynamic("row-" + strconv.Itoa(i))
		}
		// Render wraps the rows inside a Dynamic("page") container,
		// matching the real fluent-examples page.New helper.
		renderAll := func(s manyState) node.Node {
			rows := make([]node.Node, n)
			for i := range n {
				rows[i] = renderRow(i, s.Counters[i])
			}
			return div.New(rows...).Dynamic("page")
		}

		handleReset := func(_ Session, s manyState, ev Event) manyState {
			if ev.Action == "reset" {
				s.Counters = [n]int{}
			}
			return s
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		ct := newConnectedTransport()
		sess := &StatefulSession[manyState]{
			id:          "test",
			state:       manyState{},
			render:      renderAll,
			handle:      handleReset,
			engine:      differ,
			encoder:     wire.JSONEncoder{},
			transport:   ct,
			events:      make(chan Event),
			cmds:        make(chan func(), defaultCmdBufferSize),
			fxCh:        make(chan func(*Effects), defaultCmdBufferSize),
			overflowSem: make(chan struct{}, defaultCmdBufferSize),
			loopDone:    make(chan struct{}),
			destroyed:   make(chan struct{}),
			ctx:         ctx,
			stop:        cancel,
		}
		sess.attachTransportCtx()
		sess.status.Store(int32(Pending))
		differ.Render(renderAll(sess.state), io.Discard)

		go sess.readTransport(sess.events)
		go sess.run()

		// Patch rows 0..2 to value 1 (simulating ticker activity).
		for i := range 3 {
			sess.Patch("row-"+strconv.Itoa(i), func(s manyState) (manyState, node.Node) {
				s.Counters[i] = 1
				return s, renderRow(i, 1)
			})
		}
		synctest.Wait()

		ct.mu.Lock()
		ct.sent = nil
		ct.mu.Unlock()

		// Send the reset event.
		ct.ch <- Event{Type: "click", Action: "reset"}
		synctest.Wait()

		ct.mu.Lock()
		sent := ct.sent
		ct.mu.Unlock()

		if len(sent) == 0 {
			t.Fatal("reset event produced no messages; expected patches or a full morph that undoes the previous patches")
		}

		// Verify each previously-patched row is reset on the client.
		for i := range 3 {
			key := "row-" + strconv.Itoa(i)
			expected := "Counter " + strconv.Itoa(i) + ": 0"
			found := false
			for _, msg := range sent {
				s := string(msg)
				if strings.Contains(s, expected) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("reset did not produce an update for %s showing %q\nsent: %s", key, expected, sent)
			}
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestPatchResetWithManyKeys verifies that after patching a subset of
// Dynamic-keyed rows via DiffKey, a Handle event that mutates state
// produces a full Diff that emits patches for each row whose snapshot
// no longer matches the new render output.
//
// This mirrors the fluent-examples/tether patch demo's state shape
// (20 counters, reset zeroes all). Unlike the real demo, this test
// uses sequential Patch calls (no background goroutine) to validate
// the core Patch→Handle→Diff interaction in isolation.
func TestPatchResetWithManyKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 20

		// Render a list of n counters with stable Dynamic keys, where
		// the state struct holds all values. This mirrors the patch
		// demo's State struct and Render function.
		type manyState struct {
			Counters [n]int
		}

		renderRow := func(i, value int) node.Node {
			return span.Textf("Counter %d: %d", i, value).
				Dynamic("row-" + strconv.Itoa(i))
		}
		renderAll := func(s manyState) node.Node {
			rows := make([]node.Node, n)
			for i := range n {
				rows[i] = renderRow(i, s.Counters[i])
			}
			return div.New(rows...)
		}

		handleReset := func(_ Session, s manyState, ev Event) manyState {
			if ev.Action == "reset" {
				s.Counters = [n]int{}
			}
			return s
		}

		differ := jit.NewDiffer()
		ctx, cancel := context.WithCancel(context.Background())
		ct := newConnectedTransport()
		sess := &StatefulSession[manyState]{
			id:          "test",
			state:       manyState{},
			render:      renderAll,
			handle:      handleReset,
			engine:      differ,
			encoder:     wire.JSONEncoder{},
			transport:   ct,
			events:      make(chan Event),
			cmds:        make(chan func(), defaultCmdBufferSize),
			fxCh:        make(chan func(*Effects), defaultCmdBufferSize),
			overflowSem: make(chan struct{}, defaultCmdBufferSize),
			loopDone:    make(chan struct{}),
			destroyed:   make(chan struct{}),
			ctx:         ctx,
			stop:        cancel,
		}
		sess.attachTransportCtx()
		sess.status.Store(int32(Pending))
		differ.Render(renderAll(sess.state), io.Discard)

		go sess.readTransport(sess.events)
		go sess.run()

		// Step 1: patch rows 0..9 to simulate the ticker incrementing
		// each counter to 1 via sess.Patch. This mirrors what the
		// demo's startTicker goroutine does.
		for i := range 10 {
			sess.Patch("row-"+strconv.Itoa(i), func(s manyState) (manyState, node.Node) {
				s.Counters[i] = 1
				return s, renderRow(i, 1)
			})
		}
		synctest.Wait()

		// Clear sent messages so we only see what the reset produces.
		ct.mu.Lock()
		ct.sent = nil
		ct.mu.Unlock()

		// Step 2: send the reset event. Handle returns all zeros,
		// and the full render should emit a patch for every row that
		// was previously patched to 1.
		ct.ch <- Event{Type: "click", Action: "reset"}
		synctest.Wait()

		ct.mu.Lock()
		sent := ct.sent
		ct.mu.Unlock()

		if len(sent) == 0 {
			t.Fatal("reset event produced no messages; expected patches for rows 0..9")
		}

		// Count how many of rows 0..9 got a patch back to value 0.
		seen := make(map[string]bool)
		for _, msg := range sent {
			s := string(msg)
			for i := range 10 {
				key := "row-" + strconv.Itoa(i)
				expected := "Counter " + strconv.Itoa(i) + ": 0"
				if strings.Contains(s, key) && strings.Contains(s, expected) {
					seen[key] = true
				}
			}
		}
		if len(seen) != 10 {
			t.Errorf("expected patches for all 10 previously-patched rows, got %d: %v\nsent messages: %s", len(seen), seen, sent)
		}

		sess.stop()
		synctest.Wait()
	})
}
