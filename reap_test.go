package poly

import (
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	jit "github.com/jpl-au/fluent-jit"
)

func TestReaperRemovesPendingSessionAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &Handler[counterState]{
			cfg: Config[counterState]{
				PendingTimeout: 100 * time.Millisecond,
				ReaperInterval: 50 * time.Millisecond,
				Logger:         slog.Default(),
			},
			pending: map[string]*pendingSession[counterState]{
				"p1": {
					state:     counterState{},
					differ:    jit.NewDiffer(),
					createdAt: time.Now(),
				},
			},
			active:       make(map[string]*Session[counterState]),
			disconnected: make(map[string]*Session[counterState]),
			done:         make(chan struct{}),
		}

		go h.reap()

		// Advance past PendingTimeout. The ticker fires every 50ms;
		// after 150ms at least one reap cycle runs after the 100ms
		// timeout has elapsed.
		time.Sleep(150 * time.Millisecond)
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

func TestReaperRemovesDisconnectedSessionAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.logger = slog.Default()
		sess.disconnectedAt = time.Now()

		h := &Handler[counterState]{
			cfg: Config[counterState]{
				ReconnectTimeout: 200 * time.Millisecond,
				ReaperInterval:   50 * time.Millisecond,
				Logger:           slog.Default(),
			},
			pending: make(map[string]*pendingSession[counterState]),
			active:  make(map[string]*Session[counterState]),
			disconnected: map[string]*Session[counterState]{
				sess.id: sess,
			},
			done: make(chan struct{}),
		}

		go h.reap()

		// Advance past ReconnectTimeout so the reaper evicts the session.
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		h.mu.Lock()
		n := len(h.disconnected)
		h.mu.Unlock()

		if n != 0 {
			t.Errorf("expected 0 disconnected sessions after timeout, got %d", n)
		}

		close(h.done)
	})
}

func TestReaperClosesIdleSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.logger = slog.Default()
		sess.lastActivity = time.Now()
		sess.createdAt = time.Now()

		h := &Handler[counterState]{
			cfg: Config[counterState]{
				IdleTimeout:    200 * time.Millisecond,
				ReaperInterval: 50 * time.Millisecond,
				Logger:         slog.Default(),
			},
			pending: make(map[string]*pendingSession[counterState]),
			active: map[string]*Session[counterState]{
				sess.id: sess,
			},
			disconnected: make(map[string]*Session[counterState]),
			done:         make(chan struct{}),
		}

		go h.reap()

		// Advance past IdleTimeout so the reaper closes the session.
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		mt.mu.Lock()
		closed := mt.closed
		mt.mu.Unlock()

		if !closed {
			t.Error("expected transport to be closed after idle timeout")
		}

		h.mu.Lock()
		n := len(h.active)
		h.mu.Unlock()

		if n != 0 {
			t.Errorf("expected 0 active sessions after idle reap, got %d", n)
		}

		close(h.done)
	})
}

func TestReaperClosesSessionAtMaxLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.logger = slog.Default()
		sess.lastActivity = time.Now()
		sess.createdAt = time.Now()

		h := &Handler[counterState]{
			cfg: Config[counterState]{
				MaxLifetime:    200 * time.Millisecond,
				ReaperInterval: 50 * time.Millisecond,
				Logger:         slog.Default(),
			},
			pending: make(map[string]*pendingSession[counterState]),
			active: map[string]*Session[counterState]{
				sess.id: sess,
			},
			disconnected: make(map[string]*Session[counterState]),
			done:         make(chan struct{}),
		}

		go h.reap()

		// Advance past MaxLifetime so the reaper closes the session.
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()

		mt.mu.Lock()
		closed := mt.closed
		mt.mu.Unlock()

		if !closed {
			t.Error("expected transport to be closed after max lifetime")
		}

		h.mu.Lock()
		n := len(h.active)
		h.mu.Unlock()

		if n != 0 {
			t.Errorf("expected 0 active sessions after lifetime reap, got %d", n)
		}

		close(h.done)
	})
}

func TestReaperDoesNotCloseActiveSessionBeforeTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mt := &mockTransport{events: []Event{}}
		sess := newTestSession(counterState{Count: 0}, mt)
		sess.logger = slog.Default()
		sess.lastActivity = time.Now()
		sess.createdAt = time.Now()

		h := &Handler[counterState]{
			cfg: Config[counterState]{
				IdleTimeout:    500 * time.Millisecond,
				ReaperInterval: 50 * time.Millisecond,
				Logger:         slog.Default(),
			},
			pending: make(map[string]*pendingSession[counterState]),
			active: map[string]*Session[counterState]{
				sess.id: sess,
			},
			disconnected: make(map[string]*Session[counterState]),
			done:         make(chan struct{}),
		}

		go h.reap()

		// Advance to just before the idle timeout. The reaper should
		// have run at least once but the session should still be active.
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()

		mt.mu.Lock()
		closed := mt.closed
		mt.mu.Unlock()

		if closed {
			t.Error("transport should not be closed before idle timeout")
		}

		h.mu.Lock()
		n := len(h.active)
		h.mu.Unlock()

		if n != 1 {
			t.Errorf("expected 1 active session before timeout, got %d", n)
		}

		close(h.done)
	})
}
