package tether

import (
	"net/http"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// reattach reconnects a disconnected session with a new transport.
// A command is sent to the session's loop to swap in the new transport
// and re-render. This avoids any locking — only the loop touches
// session state.
func (h *Handler[S]) reattach(sess *StatefulSession[S], transport Transport) {
	// Stop the disconnect timer before writing callback fields
	// to avoid a data race between wireDisconnect (this goroutine)
	// and the timer callback (timer goroutine). Timer.Stop is
	// goroutine-safe; the field read is safe via h.mu happens-before.
	if sess.disconnectTimer != nil {
		sess.disconnectTimer.Stop()
		sess.disconnectTimer = nil
	}

	if hb, ok := transport.(Heartbeater); ok && !h.cfg.Timeouts.DisableHeartbeat {
		hb.StartHeartbeat(h.cfg.Timeouts.Heartbeat)
	}

	h.wireDisconnect(sess)

	select {
	case sess.cmds <- func() {

		sess.transport = transport
		sess.events = make(chan Event)
		go sess.readTransport(sess.events)

		// Re-render and send full state to catch the client up.
		// Include the last URL and title so the browser's address bar
		// and document title are synced — they live outside the DOM
		// and would otherwise be stale after reconnection.
		tree := sess.render(sess.state)
		html := sess.differ.Render(tree)
		u := wire.Update{
			Morphs: []wire.Morph{{Key: "", HTML: html}},
		}
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
// are rebuilt from scratch — the only things carried over from the
// frozen stub are the ID, endpoint, user-agent, and metadata.
func (h *Handler[S]) thaw(sess *StatefulSession[S], r *http.Request, transport Transport) {
	// Stop the disconnect timer — the client is back.
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
		// Cannot restore — destroy the frozen stub.
		h.mu.Lock()
		delete(h.active, sess.id)
		h.mu.Unlock()
		h.destroySession(sess)
		transport.Close()
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
		h.mu.Unlock()
		h.destroySession(sess)
		transport.Close()
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
		h.mu.Unlock()
		h.destroySession(sess)
		transport.Close()
		return
	}

	// Rebuild the session's runtime state.
	differ := jit.NewDiffer()
	tree := h.cfg.Render(state)
	differ.Render(tree)

	sess.state = state
	sess.differ = differ
	sess.transport = transport
	sess.events = make(chan Event)
	sess.cmds = make(chan func(), h.cfg.Limits.CmdBufferSize)
	sess.fxCh = make(chan func(*Effects), h.cfg.Limits.CmdBufferSize)
	sess.overflowSem = make(chan struct{}, h.cfg.Limits.CmdBufferSize)
	sess.loopDone = make(chan struct{})
	sess.destroyed = make(chan struct{})
	sess.lastURL = env.URL
	sess.lastTitle = env.Title

	h.wireDisconnect(sess)

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

// wireDisconnect installs the callback that moves a session into the
// disconnected pool (when reconnection is enabled) or removes it
// entirely. Called each time a transport is attached because the
// callback captures the handler's pool references.
func (h *Handler[S]) wireDisconnect(sess *StatefulSession[S]) {
	sess.onDisconnect = func() {
		h.mu.Lock()
		delete(h.active, sess.id)
		destroy := h.cfg.Timeouts.DisableReconnect
		if !destroy {
			h.disconnected[sess.id] = sess
		}
		h.mu.Unlock()

		// destroySession calls g.Remove which may fire OnLeave
		// callbacks — run it outside h.mu to avoid deadlock if
		// the callback accesses the Handler (e.g. Health).
		if destroy {
			h.destroySession(sess)
		}

		if h.cfg.OnDisconnect != nil {
			h.cfg.OnDisconnect(sess)
		}
	}

	sess.onTimeout = func() {
		h.mu.Lock()
		delete(h.disconnected, sess.id)
		h.mu.Unlock()
		h.destroySession(sess)
	}
}
