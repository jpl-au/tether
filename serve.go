package tether

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/tether/dev"
)

// serveInitialPage handles the initial GET request. It pre-warms the
// session with the diff state and embeds the session ID in the root
// element so the client can reclaim it when the transport connects.
func (h *Handler[S]) serveInitialPage(w http.ResponseWriter, r *http.Request) {
	// No origin check on the initial page GET — it is a safe method.
	// MaxPending caps pre-warmed sessions to prevent resource exhaustion
	// from cross-origin <img> tag abuse.
	if h.draining.Load() {
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			slog.Error("panic in initial render", "panic", v, "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	dev.Debug("serving initial page", "path", r.URL.Path, "remote", r.RemoteAddr)

	h.mu.Lock()
	if h.cfg.Limits.MaxPending > 0 && len(h.pending) >= h.cfg.Limits.MaxPending {
		h.mu.Unlock()
		http.Error(w, "too many pending sessions", http.StatusServiceUnavailable)
		return
	}
	if h.cfg.Limits.MaxSessions > 0 && len(h.pending)+len(h.active)+len(h.disconnected) >= h.cfg.Limits.MaxSessions {
		h.mu.Unlock()
		http.Error(w, "too many sessions", http.StatusServiceUnavailable)
		return
	}
	h.mu.Unlock()

	state := h.cfg.InitialState(r)
	if h.cfg.OnNavigate != nil {
		params := Params{
			Path:  r.URL.Path,
			Query: r.URL.Query(),
		}
		cs := &CaptureSession{SessionID: "pre-warm", PushErr: ErrPushPreWarm}
		state = h.cfg.OnNavigate(cs, state, params)
	}
	tree := h.cfg.Render(state)

	differ := jit.NewDiffer()
	html := differ.Render(tree)

	id := newID()
	now := time.Now()
	h.mu.Lock()
	h.pending[id] = &pendingSession[S]{state: state, differ: differ, createdAt: now, userAgent: r.UserAgent()}
	h.mu.Unlock()

	var pushKey string
	if h.cfg.Push != nil && h.cfg.Push.Sender != nil {
		pushKey = h.cfg.Push.Sender.PublicKey()
	}

	content := &tetherBody{
		html:              html,
		endpoint:          r.URL.Path,
		session:           id,
		transport:         h.cfg.Mode,
		retryDelay:        h.cfg.Timeouts.Retry,
		maxRetryDelay:     h.cfg.Timeouts.MaxRetry,
		defaultDebounce:   h.app.Client.DefaultDebounce,
		transitionTimeout: h.app.Client.TransitionTimeout,
		flashDuration:     h.app.Client.FlashDuration,
		toastDuration:     h.app.Client.ToastDuration,
		worker:            h.cfg.Worker,
		pushKey:           pushKey,
		backgroundSync:    h.app.Client.BackgroundSync,
		syncRetention:     h.app.Client.SyncRetention,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	// Prevent session ID leakage via Referer header on external links.
	w.Header().Set("Referrer-Policy", "same-origin")
	if dev.Enabled() {
		w.Header().Set("Cache-Control", "no-store")
	}
	if h.cfg.Layout != nil {
		h.cfg.Layout(state, content).Render(w)
	} else {
		content.Render(w)
	}
}

// serveSession upgrades the connection and starts the session command
// loop. It checks pools in priority order — disconnected first (so a
// reconnecting client recovers its state), then pending (the normal
// path after a page load), and finally creates a fresh session as a
// fallback for direct transport connections without a prior GET.
func (h *Handler[S]) serveSession(w http.ResponseWriter, r *http.Request, upgrade func(http.ResponseWriter, *http.Request) (Transport, error)) {
	// Resolve the effective protocol for this request. In Auto mode,
	// detect from the wire; in explicit mode, trust the config and
	// warn on mismatch.
	proto := resolveProtocol(h.cfg.Protocol, r, h.app.Logger)
	dev.Debug("session transport", "protocol", proto.String(), "remote", r.RemoteAddr)

	transport, err := upgrade(w, r)
	if err != nil {
		http.Error(w, "connection upgrade failed", http.StatusInternalServerError)
		return
	}

	// Close the transport if we exit before handing it to a session.
	// Once the session loop starts, it owns the transport lifecycle.
	started := false
	defer func() {
		if !started {
			transport.Close()
		}
	}()

	id := r.URL.Query().Get("session")

	// Try to reattach to a disconnected session.
	h.mu.Lock()
	if sess, ok := h.disconnected[id]; ok {
		if !h.app.Security.DisableSessionBinding && r.UserAgent() != sess.userAgent {
			h.mu.Unlock()
			h.Diagnostics.Publish(Diagnostic{
				Kind:      SessionBindingFailed,
				SessionID: id,
				Detail:    "user-agent mismatch on reconnect",
			})
			return
		}
		frozen := Status(sess.status.Load()) == Frozen
		delete(h.disconnected, id)
		h.active[id] = sess
		h.mu.Unlock()

		// Clean up stored data — Render rebuilds differ snapshots,
		// and state S is already current in memory (or will be loaded
		// from the store for frozen sessions).
		if h.cfg.DiffStore != nil {
			if err := h.cfg.DiffStore.Delete(sess.ctx, id); err != nil {
				dev.Warn("store delete failed on reconnect", "session", id, "error", err)
				h.Diagnostics.Publish(Diagnostic{
					Kind:      StoreError,
					SessionID: id,
					Err:       err,
					Detail:    "delete",
				})
			}
		}

		started = true
		if frozen {
			h.thaw(sess, r, transport)
		} else {
			if h.cfg.SessionStore != nil {
				if err := h.cfg.SessionStore.Delete(sess.ctx, id); err != nil {
					dev.Warn("session store delete failed on reconnect", "session", id, "error", err)
					h.Diagnostics.Publish(Diagnostic{
						Kind:      SessionStoreError,
						SessionID: id,
						Err:       err,
						Detail:    "delete",
					})
				}
			}
			dev.Debug("session reattached", "session", id, "endpoint", sess.endpoint, "remote", r.RemoteAddr)
			h.reattach(sess, transport)
			// Block so the HTTP goroutine stays alive — SSE
			// transports need r.Context() to remain valid.
			<-sess.loopDone
		}
		return
	}
	h.mu.Unlock()

	// Try to restore from the session store (crash recovery / node
	// migration). The client has an ID that isn't in any in-memory
	// pool, but the store may have persisted state from a previous
	// server instance.
	if h.cfg.SessionStore != nil {
		if restored, ok := h.restoreSession(id, r, transport); ok {
			started = true
			_ = restored // restoreSession blocked until the loop exited
			return
		}
	}

	// Try to claim a pending (pre-warmed) session.
	var state S
	var differ *jit.Differ

	h.mu.Lock()
	if ps, ok := h.pending[id]; ok {
		if !h.app.Security.DisableSessionBinding && r.UserAgent() != ps.userAgent {
			delete(h.pending, id)
			h.mu.Unlock()
			h.Diagnostics.Publish(Diagnostic{
				Kind:      SessionBindingFailed,
				SessionID: id,
				Detail:    "user-agent mismatch on session claim",
			})
			return
		}
		state = ps.state
		differ = ps.differ
		delete(h.pending, id)
	}
	h.mu.Unlock()

	if differ == nil {
		// Direct transport connection without a prior GET (e.g. bogus
		// or missing session ID). Reject during drain.
		if h.draining.Load() {
			return
		}

		// Enforce MaxSessions.
		if h.cfg.Limits.MaxSessions > 0 {
			h.mu.Lock()
			full := len(h.pending)+len(h.active)+len(h.disconnected) >= h.cfg.Limits.MaxSessions
			h.mu.Unlock()
			if full {
				return
			}
		}

		id = newID()
		state = h.cfg.InitialState(r)
		if h.cfg.OnNavigate != nil {
			params := Params{
				Path:  r.URL.Path,
				Query: r.URL.Query(),
			}
			cs := &CaptureSession{SessionID: "pre-warm", PushErr: ErrPushPreWarm}
			state = h.cfg.OnNavigate(cs, state, params)
		}
		differ = jit.NewDiffer()

		tree := h.cfg.Render(state)
		differ.Render(tree)
	}

	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	sess := &LiveSession[S]{
		id:               id,
		state:            state,
		render:           h.cfg.Render,
		handle:           h.cfg.Handle,
		differ:           differ,
		encoder:          h.encoder,
		transport:        transport,
		events:           make(chan Event),
		cmds:             make(chan func(), h.cfg.Limits.CmdBufferSize),
		fxCh:             make(chan func(*Effects), h.cfg.Limits.CmdBufferSize),
		overflowSem:      make(chan struct{}, h.cfg.Limits.CmdBufferSize),
		loopDone:         make(chan struct{}),
		destroyed:        make(chan struct{}),
		ctx:              ctx,
		stop:             cancel,
		createdAt:        now,
		endpoint:         r.URL.Path,
		userAgent:        r.UserAgent(),
		idleTimeout:      h.cfg.Timeouts.Idle,
		reconnectTimeout: h.cfg.Timeouts.Reconnect,
		diagnostics:      h.Diagnostics,
		store:            h.cfg.DiffStore,
		sessionStore:     h.cfg.SessionStore,
		codec:            h.sessionCodec(),
		freeze:           h.cfg.FreezeOnDisconnect && h.cfg.SessionStore != nil,
	}
	sess.lastActivity.Store(now.UnixNano())
	if len(h.cfg.Components) > 0 {
		sess.mounts = h.cfg.Components
	}
	dev.Debug("session created", "session", id, "endpoint", r.URL.Path, "remote", r.RemoteAddr)

	if h.cfg.Push != nil && h.cfg.Push.Sender != nil {
		sess.pushSender = h.cfg.Push.Sender
	}
	if h.cfg.Equal != nil {
		sess.equal = h.cfg.Equal
	}
	if h.cfg.OnStructuralChange != nil {
		sess.onStructuralChange = h.cfg.OnStructuralChange
	}
	if h.cfg.OnNoPatch != nil {
		sess.onNoPatch = h.cfg.OnNoPatch
	}
	if h.cfg.Timeouts.MaxLifetime > 0 {
		sess.lifetimeTimer = time.AfterFunc(h.cfg.Timeouts.MaxLifetime, func() {
			sess.stop()
		})
	}
	sess.startTimers()

	h.mu.Lock()
	h.active[id] = sess
	h.mu.Unlock()

	h.wireDisconnect(sess)

	// Start the command loop before OnConnect so that State(),
	// Update(), Signal(), and other methods that route through the
	// cmds channel work inside the callback. Transport reading is
	// deferred until after OnConnect to guarantee that no client
	// events are processed before subscriptions are set up.
	started = true
	go sess.run()

	// Mount components that implement Mounter before any events arrive.
	// Uses Update so side effects (Toast, Signal) are rendered and sent.
	if len(h.cfg.Components) > 0 {
		sess.Update(func(s S) S {
			return InitMounts(h.cfg.Components, sess, s)
		})
	}

	for _, w := range h.cfg.Watchers {
		w.subscribe(sess)
	}
	if len(h.cfg.Watchers) > 0 {
		dev.Debug("subscribed watchers", "session", sess.id, "endpoint", sess.endpoint, "count", len(h.cfg.Watchers))
	}

	if h.cfg.OnConnect != nil {
		dev.Debug("calling OnConnect", "session", sess.id, "endpoint", sess.endpoint)
		h.cfg.OnConnect(sess)
	}

	for _, g := range h.cfg.Groups {
		g.Add(sess)
	}
	if len(h.cfg.Groups) > 0 {
		dev.Debug("joined groups", "session", sess.id, "endpoint", sess.endpoint, "count", len(h.cfg.Groups))
	}

	// Start keep-alive writes for transports that need them (SSE).
	// For reconnects, reattach handles this.
	if hb, ok := transport.(Heartbeater); ok && !h.cfg.Timeouts.DisableHeartbeat {
		hb.StartHeartbeat(h.cfg.Timeouts.Heartbeat)
	}

	go sess.readTransport(sess.events)
	dev.Debug("session ready", "session", sess.id, "endpoint", sess.endpoint)

	// Block until the session loop exits. The HTTP handler goroutine
	// must stay alive to keep r.Context() valid — both the WebSocket
	// and SSE transports use it for reads and writes. If we returned
	// here, net/http would cancel the context and kill the connection
	// immediately.
	<-sess.loopDone
}

// sessionCodec returns the codec for serialising state S. Uses the
// developer's custom codec if configured, otherwise falls back to
// the default CBOR implementation.
func (h *Handler[S]) sessionCodec() SessionCodec[S] {
	if h.cfg.Codec != nil {
		return h.cfg.Codec
	}
	return cborCodec[S]{}
}

// restoreSession attempts to recover a session from the SessionStore.
// Returns the restored session and true on success, or nil and false
// if the store has no data for this ID or restoration fails. The
// caller should treat a false return as "no session to restore" and
// continue with normal session creation.
func (h *Handler[S]) restoreSession(id string, r *http.Request, transport Transport) (*LiveSession[S], bool) {
	data, err := h.cfg.SessionStore.Load(r.Context(), id)
	if err != nil {
		dev.Warn("session store load failed", "session", id, "error", err)
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: id,
			Err:       err,
			Detail:    "load",
		})
		return nil, false
	}
	if data == nil {
		return nil, false
	}

	env, err := unmarshalEnvelope(data)
	if err != nil {
		dev.Warn("session envelope unmarshal failed", "session", id, "error", err)
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: id,
			Err:       err,
			Detail:    "envelope",
		})
		return nil, false
	}

	// Verify the reconnecting client matches the original session.
	if !h.app.Security.DisableSessionBinding && r.UserAgent() != env.UserAgent {
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionBindingFailed,
			SessionID: id,
			Detail:    "user-agent mismatch on restore",
		})
		return nil, false
	}

	codec := h.sessionCodec()
	state, err := codec.Unmarshal(env.State)
	if err != nil {
		dev.Warn("session state unmarshal failed", "session", id, "error", err)
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: id,
			Err:       err,
			Detail:    "unmarshal",
		})
		return nil, false
	}

	// Build a fresh session with the restored state.
	differ := jit.NewDiffer()
	tree := h.cfg.Render(state)
	differ.Render(tree)

	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	sess := &LiveSession[S]{
		id:               id,
		state:            state,
		render:           h.cfg.Render,
		handle:           h.cfg.Handle,
		differ:           differ,
		encoder:          h.encoder,
		transport:        transport,
		events:           make(chan Event),
		cmds:             make(chan func(), h.cfg.Limits.CmdBufferSize),
		fxCh:             make(chan func(*Effects), h.cfg.Limits.CmdBufferSize),
		overflowSem:      make(chan struct{}, h.cfg.Limits.CmdBufferSize),
		loopDone:         make(chan struct{}),
		destroyed:        make(chan struct{}),
		ctx:              ctx,
		stop:             cancel,
		createdAt:        now,
		endpoint:         env.Endpoint,
		userAgent:        env.UserAgent,
		idleTimeout:      h.cfg.Timeouts.Idle,
		reconnectTimeout: h.cfg.Timeouts.Reconnect,
		diagnostics:      h.Diagnostics,
		store:            h.cfg.DiffStore,
		sessionStore:     h.cfg.SessionStore,
		codec:            codec,
		freeze:           h.cfg.FreezeOnDisconnect && h.cfg.SessionStore != nil,
	}
	sess.lastURL = env.URL
	sess.lastTitle = env.Title
	sess.lastActivity.Store(now.UnixNano())
	if len(h.cfg.Components) > 0 {
		sess.mounts = h.cfg.Components
	}

	if h.cfg.Push != nil && h.cfg.Push.Sender != nil {
		sess.pushSender = h.cfg.Push.Sender
	}
	if h.cfg.Equal != nil {
		sess.equal = h.cfg.Equal
	}
	if h.cfg.OnStructuralChange != nil {
		sess.onStructuralChange = h.cfg.OnStructuralChange
	}
	if h.cfg.OnNoPatch != nil {
		sess.onNoPatch = h.cfg.OnNoPatch
	}
	if h.cfg.Timeouts.MaxLifetime > 0 {
		sess.lifetimeTimer = time.AfterFunc(h.cfg.Timeouts.MaxLifetime, func() {
			sess.stop()
		})
	}
	sess.startTimers()

	h.mu.Lock()
	h.active[id] = sess
	h.mu.Unlock()

	h.wireDisconnect(sess)

	go sess.run()

	// Mount components before events arrive.
	if len(h.cfg.Components) > 0 {
		sess.Update(func(s S) S {
			return InitMounts(h.cfg.Components, sess, s)
		})
	}

	for _, w := range h.cfg.Watchers {
		w.subscribe(sess)
	}

	// Fire OnRestore if set, otherwise fall back to OnConnect.
	if h.cfg.OnRestore != nil {
		dev.Debug("calling OnRestore", "session", sess.id, "endpoint", sess.endpoint)
		h.cfg.OnRestore(sess)
	} else if h.cfg.OnConnect != nil {
		dev.Debug("calling OnConnect (restore fallback)", "session", sess.id, "endpoint", sess.endpoint)
		h.cfg.OnConnect(sess)
	}

	for _, g := range h.cfg.Groups {
		g.Add(sess)
	}

	if hb, ok := transport.(Heartbeater); ok && !h.cfg.Timeouts.DisableHeartbeat {
		hb.StartHeartbeat(h.cfg.Timeouts.Heartbeat)
	}

	// Clean up the store entry now that the session is in memory.
	if err := h.cfg.SessionStore.Delete(ctx, id); err != nil {
		dev.Warn("session store delete failed after restore", "session", id, "error", err)
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionStoreError,
			SessionID: id,
			Err:       err,
			Detail:    "delete",
		})
	}

	go sess.readTransport(sess.events)
	dev.Debug("session restored", "session", sess.id, "endpoint", sess.endpoint)

	<-sess.loopDone
	return sess, true
}
