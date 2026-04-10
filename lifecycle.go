package tether

import (
	"context"
	"net/http"
	"sync"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// reattach reconnects a disconnected session with a new transport.
// A command is sent to the session's loop to swap in the new transport
// and re-render. This avoids any locking - only the loop touches
// session state.
func (h *Handler[S]) reattach(sess *StatefulSession[S], transport Transport) {
	// Stop the disconnect timer so the session isn't destroyed
	// while we're reattaching. Timer.Stop is goroutine-safe.
	if sess.disconnectTimer != nil {
		sess.disconnectTimer.Stop()
		sess.disconnectTimer = nil
	}

	if hb, ok := transport.(Heartbeater); ok && !h.cfg.Timeouts.DisableHeartbeat {
		hb.StartHeartbeat(h.cfg.Timeouts.Heartbeat)
	}

	// Try to restore differ snapshots from the store so the
	// reconnecting client receives targeted patches instead of a
	// full root morph. If the store has no data (or Import fails),
	// the differ stays unseeded and we fall back to a full morph.
	// Delete after Load so the data is consumed exactly once.
	var diffData []byte
	if h.cfg.DiffStore != nil {
		if data, err := h.cfg.DiffStore.Load(sess.ctx, sess.id); err != nil {
			dev.Warn("diff store load failed on reconnect", "session", sess.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      StoreError,
				SessionID: sess.id,
				Err:       err,
				Detail:    "load",
			})
		} else {
			diffData = data
		}
		if err := h.cfg.DiffStore.Delete(sess.ctx, sess.id); err != nil {
			dev.Warn("diff store delete failed on reconnect", "session", sess.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      StoreError,
				SessionID: sess.id,
				Err:       err,
				Detail:    "delete",
			})
		}
	}

	select {
	case sess.cmds <- func() {

		sess.transport = transport
		sess.transportCtx, sess.transportCancel = context.WithCancel(sess.ctx)
		sess.events = make(chan Event)
		go sess.readTransport(sess.events)

		// Restore differ snapshots if available.
		if diffData != nil {
			if err := sess.engine.Import(diffData); err != nil {
				dev.Warn("differ import failed, sending full morph",
					"session", sess.id, "error", err)
			}
		}

		// Re-render and send the minimal update to catch the client
		// up. When the differ has snapshots (from Import or still in
		// memory), Diff produces targeted patches. Otherwise, Render
		// sends a full morph. URL and title are always included so
		// the browser's address bar and document title stay in sync.
		tree := sess.render(sess.state)
		patches, change := sess.engine.Diff(tree)

		var u wire.Update
		switch {
		case change != nil:
			// Structural change - full morph required.
			html := sess.engine.Render(tree)
			u.Morphs = []wire.Morph{{Key: "", HTML: html}}
		case patches == nil:
			// Unseeded differ (no snapshots) - full morph.
			html := sess.engine.Render(tree)
			u.Morphs = []wire.Morph{{Key: "", HTML: html}}
		case len(patches) > 0:
			wp := make([]wire.Patch, len(patches))
			for i, p := range patches {
				wp[i] = wire.Patch{Key: p.Key, HTML: p.HTML}
			}
			u.Patches = wp
		}
		// Empty non-nil patches means nothing changed - still send
		// URL and title below to sync the address bar.

		if sess.lastURL != "" {
			u.URL = sess.lastURL
			u.Replace = true // sync state, don't push a history entry
		}
		if sess.lastTitle != "" {
			u.Title = sess.lastTitle
		}
		sess.send(u)
	}:
	case <-sess.ctx.Done():
		transport.Close()
	}
}

// thaw restores a frozen session from the SessionStore and starts a
// new command loop. The session's state, differ, channels, and timers
// are rebuilt from scratch - the only things carried over from the
// frozen stub are the ID, endpoint, user-agent, and metadata.
func (h *Handler[S]) thaw(sess *StatefulSession[S], r *http.Request, transport Transport) {
	// Stop the disconnect timer - the client is back.
	if sess.disconnectTimer != nil {
		sess.disconnectTimer.Stop()
		sess.disconnectTimer = nil
	}

	// Load state from the store.
	data, err := h.cfg.SessionStore.Load(r.Context(), sess.id)
	if err != nil || data == nil {
		if err != nil {
			dev.Warn("session store load failed on thaw", "session", sess.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      SessionStoreError,
				SessionID: sess.id,
				Err:       err,
				Detail:    "load",
			})
		}
		// Cannot restore - destroy the frozen stub.
		h.mu.Lock()
		delete(h.active, sess.id)
		h.notifyDrain()
		h.mu.Unlock()
		h.destroySession(sess)
		if err := transport.Close(); err != nil {
			dev.Warn("transport close failed on thaw", "session", sess.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      TransportError,
				SessionID: sess.id,
				Err:       err,
				Detail:    "thaw close",
			})
		}
		return
	}

	env, err := unmarshalEnvelope(data)
	if err != nil {
		dev.Warn("session envelope unmarshal failed on thaw", "session", sess.id, "error", err)
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: sess.id,
			Err:       err,
			Detail:    "envelope",
		})
		h.mu.Lock()
		delete(h.active, sess.id)
		h.notifyDrain()
		h.mu.Unlock()
		h.destroySession(sess)
		if err := transport.Close(); err != nil {
			dev.Warn("transport close failed on thaw", "session", sess.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      TransportError,
				SessionID: sess.id,
				Err:       err,
				Detail:    "thaw close",
			})
		}
		return
	}

	codec := h.sessionCodec()
	state, err := codec.Unmarshal(env.State)
	if err != nil {
		dev.Warn("session state unmarshal failed on thaw", "session", sess.id, "error", err)
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: sess.id,
			Err:       err,
			Detail:    "unmarshal",
		})
		h.mu.Lock()
		delete(h.active, sess.id)
		h.notifyDrain()
		h.mu.Unlock()
		h.destroySession(sess)
		if err := transport.Close(); err != nil {
			dev.Warn("transport close failed on thaw", "session", sess.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      TransportError,
				SessionID: sess.id,
				Err:       err,
				Detail:    "thaw close",
			})
		}
		return
	}

	// Rebuild the session's runtime state.
	differ := jit.NewDiffer()
	tree := h.cfg.Render(state)
	differ.Render(tree)

	sess.state = state
	sess.engine = h.engine(differ, state, true)
	sess.transport = transport
	sess.transportCtx, sess.transportCancel = context.WithCancel(sess.ctx)
	sess.events = make(chan Event)
	sess.cmds = make(chan func(), h.cfg.Limits.CmdBufferSize)
	sess.fxCh = make(chan func(*Effects), h.cfg.Limits.CmdBufferSize)
	sess.overflowSem = make(chan struct{}, h.cfg.Limits.CmdBufferSize)
	sess.loopDone = make(chan struct{})
	sess.destroyed = make(chan struct{})
	sess.destroyedOnce = sync.Once{}
	sess.lastURL = env.URL
	sess.lastTitle = env.Title

	if hb, ok := transport.(Heartbeater); ok && !h.cfg.Timeouts.DisableHeartbeat {
		hb.StartHeartbeat(h.cfg.Timeouts.Heartbeat)
	}

	// Start the new loop before OnRestore so methods work inside
	// the callback.
	go sess.run()

	if len(h.cfg.Components) > 0 {
		sess.Update(func(s S) S {
			return InitMounts(h.cfg.Components, sess, s)
		})
	}

	for _, w := range h.cfg.Watchers {
		w.subscribe(sess)
	}

	if h.cfg.OnRestore != nil {
		dev.Debug("calling OnRestore (thaw)", "session", sess.id, "endpoint", sess.endpoint)
		h.cfg.OnRestore(sess)
	} else if h.cfg.OnConnect != nil {
		dev.Debug("calling OnConnect (thaw fallback)", "session", sess.id, "endpoint", sess.endpoint)
		h.cfg.OnConnect(sess)
	}

	for _, g := range h.cfg.Groups {
		g.Add(sess)
	}

	// Clean up the store entry now that state is in memory.
	if err := h.cfg.SessionStore.Delete(sess.ctx, sess.id); err != nil {
		dev.Warn("session store delete failed after thaw", "session", sess.id, "error", err)
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: sess.id,
			Err:       err,
			Detail:    "delete",
		})
	}

	go sess.readTransport(sess.events)
	dev.Debug("session thawed", "session", sess.id, "endpoint", sess.endpoint)

	<-sess.loopDone
}

// sessionDisconnected moves a session from the active pool to the
// disconnected pool (when reconnection is enabled) or destroys it
// immediately. Called from onTransportClose on the loop goroutine.
func (h *Handler[S]) sessionDisconnected(sess *StatefulSession[S]) {
	h.mu.Lock()
	delete(h.active, sess.id)
	destroy := h.cfg.Timeouts.DisableReconnect
	if !destroy {
		h.disconnected[sess.id] = sess
	}
	h.notifyDrain()
	h.mu.Unlock()

	// destroySession calls g.Remove which may fire OnLeave
	// callbacks - run it outside h.mu to avoid deadlock if
	// the callback accesses the Handler (e.g. Health).
	if destroy {
		h.destroySession(sess)
	}

	if h.cfg.OnDisconnect != nil {
		h.cfg.OnDisconnect(sess)
	}
}

// sessionTimedOut removes a disconnected session whose reconnect
// timer has fired. Called from the timer goroutine.
func (h *Handler[S]) sessionTimedOut(sess *StatefulSession[S]) {
	h.mu.Lock()
	delete(h.disconnected, sess.id)
	h.notifyDrain()
	h.mu.Unlock()
	h.destroySession(sess)
}
