package tether

import (
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	jit "github.com/jpl-au/fluent-jit"
)

func TestPendingSessionRemovedAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &Handler[counterState]{
			app: App{Logger: slog.Default()},
			cfg: StatefulConfig[counterState]{
				Timeouts: Timeouts{Pending: 100 * time.Millisecond},
			},
			pending: map[string]*pendingSession[counterState]{
				"p1": {
					state:     counterState{},
					differ:    jit.NewDiffer(),
					createdAt: time.Now(),
				},
			},
			active:       make(map[string]*StatefulSession[counterState]),
			disconnected: make(map[string]*StatefulSession[counterState]),
			done:         make(chan struct{}),
		}

		go h.reapPending()

		// Advance past the pending check interval (10s) and PendingTimeout.
		time.Sleep(11 * time.Second)
		synctest.Wait()

		h.mu.Lock()
		n := len(h.pending)
		h.mu.Unlock()

		if n != 0 {
			t.Errorf("expected 0 pending sessions after timeout, got %d", n)
		}

		close(h.done)
	})
}

func TestIdleTimerClosesSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)
		sess.idleTimeout = 200 * time.Millisecond
		sess.startTimers()

		go sess.readTransport(sess.events)
		go sess.run()

		// Advance past the idle timeout.
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		// Session context should be cancelled.
		if sess.ctx.Err() == nil {
			t.Error("session should be expired after idle timeout")
		}
	})
}

func TestMaxLifetimeClosesSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		// Set a max lifetime timer.
		time.AfterFunc(200*time.Millisecond, func() {
			sess.stop()
		})

		go sess.readTransport(sess.events)
		go sess.run()

		// Advance past the max lifetime.
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if sess.ctx.Err() == nil {
			t.Error("session should be expired after max lifetime")
		}
	})
}

func TestIdleTimerResetsOnActivity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)
		sess.idleTimeout = 300 * time.Millisecond
		sess.startTimers()

		go sess.readTransport(sess.events)
		go sess.run()

		// Send an update at 200ms — well within the 300ms idle timeout.
		// This should reset the timer.
		time.Sleep(200 * time.Millisecond)
		sess.Update(func(s counterState) counterState {
			s.Count++
			return s
		})
		synctest.Wait()

		// At 400ms the original timer would have fired (300ms), but
		// the reset pushes it to 500ms. Session should still be alive.
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()

		if sess.ctx.Err() != nil {
			t.Error("session should still be alive — idle timer was reset by activity")
		}

		// At 600ms (500ms since last activity), the timer fires.
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()

		if sess.ctx.Err() == nil {
			t.Error("session should be expired after idle timeout with no further activity")
		}
	})
}

func TestDisconnectTimerClosesSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.reconnectTimeout = 200 * time.Millisecond

		go sess.readTransport(sess.events)
		go sess.run()

		// Wait for transport to close (EOF from empty events).
		synctest.Wait()

		// Session loop should still be running (waiting for reconnect).
		if sess.ctx.Err() != nil {
			t.Fatal("session should still be alive while waiting for reconnect")
		}

		// Advance past reconnect timeout.
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		if sess.ctx.Err() == nil {
			t.Error("session should be expired after disconnect timeout")
		}
	})
}
