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
	if s.sessionStore != nil {
		s.saveSessionState(s.ctx, s.reconnectTimeout)
	}

	if s.handler != nil {
		s.handler.sessionDisconnected(s)
	}

	// Freeze: release state and differ to reclaim memory. The store
	// holds everything needed to restore. The loop exits after this
	// returns (checked by the caller in run).
	if s.freeze {
		var zero S
		s.state = zero
		s.engine = nil
		s.transition(Frozen)
		dev.Debug("session frozen", "session", s.id, "endpoint", s.endpoint)
	}
}

// saveSessionState encodes the session's state and metadata into an
// envelope and persists it to the SessionStore. The caller provides
// the context - onTransportClose passes s.ctx (still valid during
// disconnect), Shutdown passes context.Background() (s.ctx is
// cancelled after the loop exits). Failures are logged and emitted
// as diagnostics but are non-fatal.
func (s *StatefulSession[S]) saveSessionState(ctx context.Context, ttl time.Duration) {
	stateBytes, err := s.codec.Marshal(s.state)
	if err != nil {
		dev.Warn("session state marshal failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "marshal",
		})
		return
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
		return
	}

	if err := s.sessionStore.Save(ctx, s.id, data, ttl); err != nil {
		dev.Warn("session store save failed", "session", s.id, "error", err)
		s.emitDiagnostic(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: s.id,
			Err:       err,
			Detail:    "save",
		})
	}
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
		return
	}
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
	}
	if s.lifetimeTimer != nil {
		s.lifetimeTimer.Stop()
	}
	if s.transport != nil {
		s.transport.Close()
	}
	s.transition(Destroyed)
	s.destroyedOnce.Do(func() { close(s.destroyed) })
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
