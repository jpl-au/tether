package tether

import (
	"context"
	"fmt"
	"time"

	"github.com/jpl-au/tether/dev"
)

// onTransportClose runs when the client's transport connection drops.
// It nils the transport so send() discards updates during the
// reconnect window, then persists session data to any configured
// stores - DiffStore for differ snapshots (memory optimisation) and
// SessionStore for application state (crash recovery). Persistence
// happens before the pool transition so data is safely stored before
// the session becomes visible as reconnectable.
func (s *StatefulSession[S]) onTransportClose() {
	dev.Debug("transport closed",
		"session", s.id,
		"endpoint", s.endpoint,
		"url", s.lastURL,
	)
	s.transport.Close()
	s.transport = nil
	if s.transportCancel != nil {
		s.transportCancel()
	}
	if s.freeze {
		// Stop HTTP acceptance before draining the commands already
		// acknowledged to callers. They must be included in the snapshot.
		s.lifecycleMu.Lock()
		s.freezing.Store(true)
		s.lifecycleMu.Unlock()
		defer s.freezing.Store(false)
		for range len(s.cmds) {
			if s.ctx.Err() != nil {
				break
			}
			s.runCmd(<-s.cmds)
		}
		fx := &Effects{}
		s.drainFx(fx)
		s.sendFx(fx)
		// A reattachment accepted before freezing may have installed a
		// new transport while draining. Its loop must remain alive.
		if s.transport != nil {
			if s.needsRender && s.ctx.Err() == nil {
				s.needsRender = false
				s.coalescedRender()
			}
			return
		}
	}

	if s.reconnectTimeout > 0 {
		dev.Debug("disconnect timer started",
			"session", s.id,
			"endpoint", s.endpoint,
			"timeout", s.reconnectTimeout,
		)
		s.disconnectTimer = time.AfterFunc(s.reconnectTimeout, func() {
			if s.handler != nil {
				s.handler.sessionTimedOut(s)
			} else {
				s.stop()
			}
		})
	}

	// Save differ snapshots to the store before the pool transition
	// so the data is persisted before the session becomes visible as
	// reconnectable. Export copies without clearing; Clear is only
	// called after a confirmed successful save.
	if s.store != nil {
		if data := s.engine.Export(); data != nil {
			if err := s.store.Save(s.ctx, s.id, data); err != nil {
				dev.Warn("store save failed, keeping snapshots in memory",
					"session", s.id, "error", err)
				s.emitDiagnostic(Diagnostic{
					Kind:      StoreError,
					SessionID: s.id,
					Err:       err,
					Detail:    "save",
				})
			} else {
				s.engine.Clear()
			}
		}
	}

	// Save session state for crash recovery. The codec serialises S,
	// the envelope wraps it with metadata, and the store persists the
	// bytes. TTL matches the reconnect window - if the client never
	// comes back, the store entry can expire.
	saved := s.sessionStore != nil && s.saveSessionState(s.ctx, s.reconnectTimeout)

	// Publish Frozen before the disconnected pool entry. A reconnect can
	// now wait for loopDone before rebuilding fields still owned by this loop.
	if s.freeze && saved && s.ctx.Err() == nil && s.status.CompareAndSwap(int32(Active), int32(Frozen)) {
		s.subscriptionContext()
		s.subscriptions.Load().cancel()
	}
	if s.handler != nil {
		s.handler.sessionDisconnected(s)
	}

	// Freeze: release state and differ to reclaim memory. The store
	// holds everything needed to restore. The loop exits after this
	// returns (checked by the caller in run). The snapshot is zeroed
	// too so State() reflects the released state.
	if Status(s.status.Load()) == Frozen {
		var zero S
		s.state = zero
		s.stateSnap.Store(zero)
		s.engine = nil
		s.stateReleased = true
		dev.Debug("session frozen", "session", s.id, "endpoint", s.endpoint)
	}
}

// saveSessionState encodes the session's state and metadata into an
// envelope and persists it to the SessionStore. The caller provides
// the context - onTransportClose passes s.ctx (still valid during
// disconnect), Shutdown passes context.Background() (s.ctx is
// cancelled after the loop exits). Failures are logged and emitted
// as diagnostics but are non-fatal.
func (s *StatefulSession[S]) saveSessionState(ctx context.Context, ttl time.Duration) bool {
	stateBytes, err := s.codec.Marshal(s.state)
	if err != nil {
		dev.Warn("session state marshal failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "marshal",
		})
		return false
	}

	if s.maxStateBytes > 0 && int64(len(stateBytes)) > s.maxStateBytes {
		dev.Warn("session state exceeds MaxStateBytes",
			"session", s.id,
			"size", len(stateBytes),
			"limit", s.maxStateBytes,
		)
		s.emitDiagnostic(Diagnostic{
			Kind:      StateSizeExceeded,
			SessionID: s.id,
			Err:       fmt.Errorf("state size %d exceeds limit %d", len(stateBytes), s.maxStateBytes),
			Detail:    fmt.Sprintf("%d bytes", len(stateBytes)),
		})
	}

	env := sessionEnvelope{
		State:     stateBytes,
		Endpoint:  s.endpoint,
		URL:       s.lastURL,
		Title:     s.lastTitle,
		UserAgent: s.userAgent,
	}
	data, err := marshalEnvelope(env)
	if err != nil {
		dev.Warn("session envelope marshal failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "envelope",
		})
		return false
	}

	if err := s.sessionStore.Save(ctx, s.id, data, ttl); err != nil {
		dev.Warn("session store save failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "save",
		})
		return false
	}
	return true
}

// cleanup runs when the loop exits. For frozen sessions only the idle
// timer is stopped - the disconnect timer keeps running so the reaper
// can destroy the session if it is never thawed. For destroyed
// sessions, everything is stopped and the destroyed channel is closed
// via destroyedOnce.
func (s *StatefulSession[S]) cleanup() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	if Status(s.status.Load()) == Frozen {
		// Effects held before freeze can accompany same-process thaw.
		// Later enqueues are rejected; this loop generation has finished.
		return
	}
	// Nothing will ever deliver what is still buffered: the loop is
	// leaving for good. One report covers reconnect expiry, idle
	// timeout, MaxLifetime, DisableReconnect, a dropped command and
	// Shutdown. Closures parked in overflow goroutines are not visible
	// here - they unblock on loopDone and discard themselves - so this
	// covers the held and buffered effects only.
	s.reportUndelivered()
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
	}
	if s.lifetimeTimer != nil {
		s.lifetimeTimer.Stop()
	}
	if s.transport != nil {
		s.transport.Close()
	}
	if s.transportCancel != nil {
		s.transportCancel()
	}
	// Another path may already have moved the session to Destroyed
	// (destroySession racing a freeze, Shutdown racing a thaw) - the
	// CAS simply loses and the transition is skipped.
	s.status.CompareAndSwap(int32(Active), int32(Destroyed))
	s.destroyedOnce.Do(func() { close(s.destroyed) })

	// Destruction that starts inside the session (handler panic with
	// no OnPanic, idle timeout, MaxLifetime, dropped command) exits
	// through this path without ever passing through the transport
	// disconnect flow, so the handler bookkeeping - pool removal,
	// group membership, store entries, drain notification - happens
	// here. sessionDestroyed is a no-op for sessions that were
	// already removed from the pools (disconnect timer, destroy
	// beacon, shutdown), making the two destruction paths converge.
	if s.handler != nil {
		s.handler.sessionDestroyed(s)
	}
}

// reportUndelivered emits a diagnostic for effects the session is
// carrying when its loop exits. Held effects were waiting for a
// reconnect that never came; queued closures never reached the loop.
// Both are genuine losses, and a session that dies quietly having
// swallowed a developer's Toast or Signal is the failure this
// diagnostic exists to make visible. Runs on the loop goroutine.
func (s *StatefulSession[S]) reportUndelivered() {
	held := s.heldFx != nil && s.heldFx.Any()
	queued := len(s.fxCh)
	if !held && queued == 0 {
		return
	}
	s.heldFx = nil
	s.emitDiagnostic(Diagnostic{
		Kind:      CommandDiscarded,
		SessionID: s.id,
		Detail: fmt.Sprintf("session ended with undelivered effects (held: %t, queued: %d, status: %s)",
			held, queued, Status(s.status.Load())),
	})
}

// startTimers sets up per-session lifecycle timers. Called once during
// session creation in serve.go. Timer cleanup happens in [cleanup],
// which runs as a defer in [run] when the loop exits.
func (s *StatefulSession[S]) startTimers() {
	if s.idleTimeout > 0 {
		s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
			s.stop()
		})
	}
}
