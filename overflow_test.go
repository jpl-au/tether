package tether

import (
	"testing"
	"testing/synctest"
)

// TestEnqueueOverflowSpawnsGoroutine verifies that when the command
// buffer is full, enqueue spawns an overflow goroutine to deliver
// the command and emits a BufferOverflow diagnostic.
func TestEnqueueOverflowSpawnsGoroutine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)
		// Tiny buffer so we can fill it easily.
		sess.cmds = make(chan func(), 1)
		sess.overflowSem = make(chan struct{}, 1)

		diag := NewBus[Diagnostic]()
		sess.diagnostics = diag

		var overflow bool
		diag.Subscribe(sess.ctx, func(d Diagnostic) {
			if d.Kind == BufferOverflow {
				overflow = true
			}
		})

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Send a blocking command so the loop is busy and can't
		// drain the buffer.
		block := make(chan struct{})
		sess.cmds <- func() { <-block }
		synctest.Wait()

		// Buffer has capacity 1, loop is blocked - fill it.
		sess.cmds <- func() {}

		// The next enqueue should overflow via the semaphore.
		sess.enqueue(func() {})
		synctest.Wait()

		if !overflow {
			t.Error("expected BufferOverflow diagnostic on overflow")
		}

		close(block)
		sess.stop()
		synctest.Wait()
	})
}

// TestEnqueueDropWhenExhausted verifies that when both the command
// buffer and the overflow semaphore are full, the command is dropped
// and a CommandDropped diagnostic is emitted.
func TestEnqueueDropWhenExhausted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)
		sess.cmds = make(chan func(), 1)
		sess.overflowSem = make(chan struct{}, 1)

		diag := NewBus[Diagnostic]()
		sess.diagnostics = diag

		var dropped bool
		diag.Subscribe(sess.ctx, func(d Diagnostic) {
			if d.Kind == CommandDropped {
				dropped = true
			}
		})

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Block the loop so it can't drain.
		block := make(chan struct{})
		sess.cmds <- func() { <-block }
		synctest.Wait()

		// Fill the command buffer (cap 1).
		sess.cmds <- func() {}
		// Fill the overflow semaphore (cap 1).
		sess.overflowSem <- struct{}{}

		// Both full - the next enqueue should drop.
		sess.enqueue(func() {})
		synctest.Wait()

		if !dropped {
			t.Error("expected CommandDropped diagnostic when fully exhausted")
		}

		<-sess.overflowSem
		close(block)
		sess.stop()
		synctest.Wait()
	})
}

// TestEnqueueDropCallsOnCommandDropped verifies that when
// OnCommandDropped is set, the callback fires and the session
// survives instead of being destroyed.
func TestEnqueueDropCallsOnCommandDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)
		sess.cmds = make(chan func(), 1)
		sess.overflowSem = make(chan struct{}, 1)

		var called bool
		sess.onCommandDropped = func(_ *StatefulSession[counterState]) {
			called = true
		}

		go sess.readTransport(sess.events)
		go sess.run()
		defer func() { sess.stop(); synctest.Wait() }()
		synctest.Wait()

		// Block the loop so it can't drain.
		block := make(chan struct{})
		sess.cmds <- func() { <-block }
		synctest.Wait()

		// Fill the command buffer (cap 1).
		sess.cmds <- func() {}
		// Fill the overflow semaphore (cap 1).
		sess.overflowSem <- struct{}{}

		// Both full - should call onCommandDropped instead of destroying.
		sess.enqueue(func() {})
		synctest.Wait()

		if !called {
			t.Error("OnCommandDropped callback was not called")
		}

		// Session should still be alive.
		select {
		case <-sess.ctx.Done():
			t.Error("session should not be destroyed when OnCommandDropped is set")
		default:
		}

		<-sess.overflowSem
		close(block)
	})
}

// TestEnqueueFxOverflow verifies that enqueueFx also emits a
// BufferOverflow diagnostic when the effect buffer is full.
func TestEnqueueFxOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{}, ct)
		sess.fxCh = make(chan func(*Effects), 1)
		sess.overflowSem = make(chan struct{}, 1)

		diag := NewBus[Diagnostic]()
		sess.diagnostics = diag

		var overflow bool
		diag.Subscribe(sess.ctx, func(d Diagnostic) {
			if d.Kind == BufferOverflow {
				overflow = true
			}
		})

		go sess.readTransport(sess.events)
		go sess.run()
		synctest.Wait()

		// Fill the effect buffer (cap 1). The loop drains fxCh in
		// its select, but only when no events or commands are pending.
		// Send a blocking command to keep the loop busy.
		block := make(chan struct{})
		sess.cmds <- func() { <-block }
		synctest.Wait()

		sess.fxCh <- func(*Effects) {}

		// The next enqueueFx should overflow.
		sess.enqueueFx(func(*Effects) {})
		synctest.Wait()

		if !overflow {
			t.Error("expected BufferOverflow diagnostic on effect overflow")
		}

		close(block)
		sess.stop()
		synctest.Wait()
	})
}
