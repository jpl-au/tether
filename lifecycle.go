package tether

import "github.com/jpl-au/fluent-tether/wire"

// reattach reconnects a disconnected session with a new transport.
// A command is sent to the session's loop to swap in the new transport
// and re-render. This avoids any locking — only the loop touches
// session state.
func (h *Handler[S]) reattach(sess *Session[S], transport Transport) {
	if hb, ok := transport.(heartbeater); ok && !h.cfg.Timeouts.DisableHeartbeat {
		hb.StartHeartbeat(h.cfg.Timeouts.Heartbeat)
	}

	h.wireDisconnect(sess)

	select {
	case sess.cmds <- func() {
		// Stop the disconnect timer — we're reconnecting.
		if sess.disconnectTimer != nil {
			sess.disconnectTimer.Stop()
			sess.disconnectTimer = nil
		}

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

// wireDisconnect installs the callback that moves a session into the
// disconnected pool (when reconnection is enabled) or removes it
// entirely. Called each time a transport is attached because the
// callback captures the handler's pool references.
func (h *Handler[S]) wireDisconnect(sess *Session[S]) {
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
}
