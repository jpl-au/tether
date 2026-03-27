package tether

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// TestSlowRenderDiagnosticFires uses a real (non-synctest) goroutine
// so that time.Since measures actual wall-clock duration. Synctest
// uses fake time where CPU work takes zero time, which would prevent
// the 1ns threshold from being exceeded.
func TestSlowRenderDiagnosticFires(t *testing.T) {
	ct := newConnectedTransport()
	sess := newTestSession(counterState{Count: 0}, ct)
	sess.slowRender = 1 * time.Nanosecond // any render will exceed this

	var mu sync.Mutex
	var got []Diagnostic
	sess.diagnostics = NewBus[Diagnostic]()
	sess.diagnostics.Subscribe(sess.ctx, func(d Diagnostic) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	})

	go sess.readTransport(sess.events)
	go sess.run()

	ct.ch <- Event{Type: "click", Action: "increment"}
	time.Sleep(50 * time.Millisecond)

	sess.stop()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, d := range got {
		if d.Kind == SlowRender {
			found = true
			if d.Detail == "" {
				t.Error("SlowRender diagnostic should include duration in Detail")
			}
		}
	}
	if !found {
		t.Error("expected SlowRender diagnostic when threshold is 1ns")
	}
}

func TestSlowRenderDiagnosticDisabledByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)
		// slowRender is zero (default) - no diagnostic should fire.

		var got []Diagnostic
		sess.diagnostics = NewBus[Diagnostic]()
		sess.diagnostics.Subscribe(sess.ctx, func(d Diagnostic) {
			got = append(got, d)
		})

		go sess.readTransport(sess.events)
		go sess.run()

		ct.ch <- Event{Type: "click", Action: "increment"}
		synctest.Wait()

		sess.stop()
		synctest.Wait()

		for _, d := range got {
			if d.Kind == SlowRender {
				t.Error("SlowRender diagnostic should not fire when threshold is zero")
			}
		}
	})
}
