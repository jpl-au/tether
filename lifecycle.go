package tether

import (
	"context"
	"net/http"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// reattach reconnects a disconnected session with a new transport.
// A command is sent to the session's loop to swap in the new transport
// and re-render. This avoids any locking - only the loop touches
// session state. Returns a channel that closes when this attachment's
// transport lifetime ends, so the HTTP handler goroutine can return
// as soon as the transport is gone instead of blocking until the
// session is destroyed.
func (h *Handler[S]) reattach(sess *StatefulSession[S], transport Transport, navigate ...string) <-chan struct{} {
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

	// The attachment context is created here (context creation is
	// goroutine-safe) but installed on the loop so only the loop
	// mutates session fields.
	tctx, tcancel := context.WithCancel(sess.ctx)
	finished := make(chan struct{})
	installed := make(chan struct{})
	q := sess.queue()
	loopDone := q.done
	if sess.ctx.Err() != nil || sess.freezing.Load() || Status(sess.status.Load()) == Frozen || Status(sess.status.Load()) == Destroyed {
		tcancel()
		transport.Close()
		close(finished)
		return finished
	}
	// A freeze can win after the caller looked up an active session.
	// Close the attempted attachment if its command never gets a loop.
	go func() {
		select {
		case <-installed:
		case <-loopDone:
			select {
			case <-installed:
				return
			default:
			}
			tcancel()
			transport.Close()
			close(finished)
		}
	}()

	select {
	case q.cmds <- func() {
		close(installed)
		if sess.ctx.Err() != nil {
			tcancel()
			transport.Close()
			close(finished)
			return
		}
		h.mu.Lock()
		if h.disconnected[sess.id] == sess {
			delete(h.disconnected, sess.id)
			h.active[sess.id] = sess
		}
		h.mu.Unlock()
		// Stop the disconnect timer on the loop goroutine - the
		// timer fields are loop-owned (cleanup reads them there).
		// If the timer fires before this command runs, the caller
		// has already moved the session back to the active pool, so
		// sessionTimedOut's membership recheck makes it a no-op.
		if sess.disconnectTimer != nil {
			sess.disconnectTimer.Stop()
			sess.disconnectTimer = nil
		}

		takeover := sess.transport != nil
		if sess.transportCancel != nil {
			sess.transportCancel()
		}
		if sess.transport != nil {
			sess.transport.Close()
		}
		sess.transport = transport
		sess.transportCtx.Store(&tctx)
		sess.transportCancel = tcancel
		sess.events = make(chan Event)
		readerDone := sess.startReader()
		go func() { <-tctx.Done(); <-readerDone; close(finished) }()

		// Restore differ snapshots if available.
		if diffData != nil {
			if err := sess.engine.Import(diffData); err != nil {
				dev.Warn("differ import failed, sending full morph",
					"session", sess.id, "error", err)
			}
		}

		// A Navigate raised while the client was away names a page the
		// server never rendered: OnNavigate has not run for it, so the
		// state and the target URL disagree. Resolve it here, before
		// the diff, exactly as exec does for redirects inside a
		// navigate event - the framework resolves navigation
		// server-side rather than round-tripping to the client. The
		// address bar is then synced with Replace so the client does
		// not echo a navigate event back for a page it is already on.
		if len(navigate) > 0 {
			sess.resolveReconnectNavigate(navigate[0])
		} else {
			sess.resolveHeldNavigate()
		}

		// Re-render and send the minimal update to catch the client
		// up. The baseline still describes the DOM the browser is
		// showing - renders during the disconnect window are deferred
		// precisely so it does (see deferRender) - so Diff produces
		// exactly the patches the client missed while away. An
		// unseeded engine falls back to a full morph. URL and title
		// are always included so the browser's address bar and
		// document title stay in sync.
		tree := sess.render(sess.state)
		patches, change := sess.engine.Diff(tree)

		var u wire.Update
		switch {
		case takeover || change != nil:
			// Structural change - full morph required.
			html := sess.engine.RenderBytes(tree)
			u.Morphs = []wire.Morph{{Key: "", HTML: html}}
		case patches == nil:
			// Unseeded differ (no snapshots) - full morph.
			html := sess.engine.RenderBytes(tree)
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
		// Effects held during the window ride with the patches, so the
		// client receives the state it missed and the notifications
		// that went with it in one frame. Merged after the URL and
		// title fallback so a held Title wins over the replayed one;
		// the held URL was already folded into lastURL by holdFx and
		// resolved above, so it never reaches this merge.
		if sess.heldFx != nil {
			sess.heldFx.merge(&u)
			sess.heldFx = nil
		}
		sess.send(u)
		if h.cfg.SessionStore != nil {
			if err := h.cfg.SessionStore.Delete(sess.ctx, sess.id); err != nil {
				sess.emitDiagnostic(Diagnostic{Kind: SessionStoreError, SessionID: sess.id, Err: err, Detail: "delete"})
			}
		}
	}:
	case <-sess.ctx.Done():
		// The monitor owns finished when no command is installed.
		tcancel()
		transport.Close()
	}
	return finished
}

// thaw restores a frozen session from the SessionStore and starts a
// new command loop. The session's state, differ, channels, and timers
// are rebuilt from scratch - the only things carried over from the
// frozen stub are the ID, endpoint, user-agent, and metadata.
func (h *Handler[S]) thaw(sess *StatefulSession[S], r *http.Request, transport Transport, navigate ...string) {
	h.thawReady(sess, r, transport, func() {}, navigate...)
}

func (h *Handler[S]) thawReady(sess *StatefulSession[S], r *http.Request, transport Transport, ready func(), navigate ...string) {
	// Frozen is published before OnDisconnect finishes. Wait for the old
	// loop's cleanup before accessing timers, state or queue fields.
	sess.lifecycleMu.Lock()
	oldDone := sess.loopDone
	sess.lifecycleMu.Unlock()
	select {
	case <-oldDone:
	case <-sess.ctx.Done():
		transport.Close()
		return
	case <-r.Context().Done():
		transport.Close()
		return
	}
	// Load state from the store.
	data, err := h.cfg.SessionStore.Load(r.Context(), sess.id)
	if r.Context().Err() != nil {
		transport.Close()
		return
	}
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
		delete(h.disconnected, sess.id)
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
		delete(h.disconnected, sess.id)
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
		delete(h.disconnected, sess.id)
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

	// Serialise runtime replacement with destruction. Activation is
	// published only after every field belongs to the new loop.
	sess.lifecycleMu.Lock()
	if sess.ctx.Err() != nil || Status(sess.status.Load()) != Frozen {
		sess.lifecycleMu.Unlock()
		if err := transport.Close(); err != nil {
			dev.Warn("transport close failed on thaw", "session", sess.id, "error", err)
		}
		return
	}
	if sess.disconnectTimer != nil {
		sess.disconnectTimer.Stop()
		sess.disconnectTimer = nil
	}
	h.mu.Lock()
	if h.disconnected[sess.id] == sess {
		delete(h.disconnected, sess.id)
		h.active[sess.id] = sess
	}
	h.mu.Unlock()

	// Rebuild the session's runtime state. The destroyed channel and
	// its once are deliberately not recreated - they are still armed
	// from the session's creation (freeze skips them) and destroy is
	// a one-shot event for the session's whole lifetime.
	differ := jit.NewDiffer()

	sess.state = state
	sess.stateReleased = false
	sess.stateSnap.Store(state)
	sess.engine = h.engine(differ, state, false)
	sess.transport = transport
	tctx := sess.attachTransportCtx()
	sess.events = make(chan Event)
	sess.cmds = make(chan func(), h.cfg.Limits.CmdBufferSize)
	sess.fxCh = make(chan func(*Effects), h.cfg.Limits.CmdBufferSize)
	sess.overflowSem = make(chan struct{}, h.cfg.Limits.CmdBufferSize)
	sess.loopDone = make(chan struct{})
	sess.queueSnapshot.Store(&sessionQueue{sess.cmds, sess.fxCh, sess.overflowSem, sess.loopDone})
	sess.lastURL = env.URL
	sess.lastTitle = env.Title
	ctx, cancel := context.WithCancel(sess.ctx)
	sess.subscriptions.Store(&subscriptionLifetime{ctx: ctx, cancel: cancel})
	sess.needsRender = false
	sess.startTimers()

	// The restored state can differ from the last DOM received before
	// disconnect. Seed and deliver it even when no callback changes state.
	sess.cmds <- func() {
		if len(navigate) > 0 {
			sess.resolveReconnectNavigate(navigate[0])
		}
		html := sess.engine.RenderBytes(sess.render(sess.state))
		u := wire.Update{Morphs: []wire.Morph{{Key: "", HTML: html}}, URL: sess.lastURL, Title: sess.lastTitle, Replace: true}
		if sess.heldFx != nil {
			sess.heldFx.merge(&u)
			sess.heldFx = nil
		}
		sess.send(u)
		h.clearRecovery(sess)
	}

	if hb, ok := transport.(Heartbeater); ok && !h.cfg.Timeouts.DisableHeartbeat {
		hb.StartHeartbeat(h.cfg.Timeouts.Heartbeat)
	}

	// Start the new loop before OnRestore so methods work inside
	// the callback.
	sess.transition(Active)
	reader := sess.prepareReader()
	go sess.run()
	sess.lifecycleMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			sess.emitDiagnostic(Diagnostic{Kind: HandlerPanic, SessionID: sess.id, Err: panicErr(r), Detail: "session thaw"})
			sess.stop()
		}
	}()

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

	h.joinGroups(sess)

	readerDone := reader()
	ready()
	dev.Debug("session thawed", "session", sess.id, "endpoint", sess.endpoint)

	// Hold the HTTP goroutine only for this attachment's lifetime -
	// the transport context ends on disconnect, freeze, or destroy.
	<-tctx.Done()
	<-readerDone
}

// sessionDisconnected moves a session from the active pool to the
// disconnected pool (when reconnection is enabled) or destroys it
// immediately. Called from onTransportClose on the loop goroutine.
func (h *Handler[S]) sessionDisconnected(sess *StatefulSession[S]) {
	h.mu.Lock()
	if h.active[sess.id] != sess {
		h.mu.Unlock()
		return // Shutdown or explicit destruction already claimed it.
	}
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
// timer has fired. Called from the timer goroutine. Membership in
// the disconnected pool is rechecked under the lock: if the timer
// fired at the same moment a client reconnected, reattach's
// Timer.Stop came too late to prevent this callback, but the session
// has already moved back to the active pool and must not be
// destroyed out from under the client.
func (h *Handler[S]) sessionTimedOut(sess *StatefulSession[S]) {
	h.mu.Lock()
	if _, ok := h.disconnected[sess.id]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.disconnected, sess.id)
	h.notifyDrain()
	h.mu.Unlock()
	h.destroySession(sess)
}
