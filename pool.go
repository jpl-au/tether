package poly

import (
	"context"
	"net/http"
	"sync"
	"time"

	jit "github.com/jpl-au/fluent-jit"
)

// pendingSession holds a pre-warmed session created during the initial GET
// request. The state and differ are seeded so that the WebSocket can attach
// without repeating the initial render.
type pendingSession[S any] struct {
	state     S
	differ    *jit.Differ
	createdAt time.Time
}

// defaultPendingTimeout is used when PendingTimeout is zero.
const defaultPendingTimeout = 30 * time.Second

// Handler manages the lifecycle of poly sessions. Sessions move through
// three pools — pending, active, and disconnected — so the server can
// pre-warm state on the initial GET and preserve it across brief network
// interruptions. Use Shutdown for graceful termination.
type Handler[S any] struct {
	cfg          Config[S]
	mu           sync.Mutex
	pending      map[string]*pendingSession[S]
	active       map[string]*Session[S]
	disconnected map[string]*Session[S]
	done         chan struct{}
	closeOnce    sync.Once
}

// serveInitialPage handles the initial GET request. It pre-warms the
// session with the diff state and embeds the session ID in the root
// element so the client can reclaim it when the transport connects.
func (h *Handler[S]) serveInitialPage(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			h.cfg.Logger.Error("panic in initial render", "panic", v)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	h.cfg.Logger.Info("serving initial page")

	// Disconnected sessions are excluded from the limit because they
	// are not actively consuming transport resources — they are just
	// holding state in memory while waiting for the client to reconnect.
	// Including them would cause new connections to be rejected during
	// brief network interruptions when many clients disconnect at once.
	h.mu.Lock()
	if h.cfg.MaxSessions > 0 && len(h.pending)+len(h.active) >= h.cfg.MaxSessions {
		h.mu.Unlock()
		http.Error(w, "too many sessions", http.StatusServiceUnavailable)
		return
	}
	h.mu.Unlock()

	state := h.cfg.InitialState(r)
	if h.cfg.HandleParams != nil {
		state = h.cfg.HandleParams(state, Params{
			Path:  r.URL.Path,
			Query: r.URL.Query(),
		})
	}
	tree := h.cfg.Render(state)

	differ := jit.NewDiffer()
	html := differ.Render(tree)

	id := newID()
	now := time.Now()
	h.mu.Lock()
	h.pending[id] = &pendingSession[S]{state: state, differ: differ, createdAt: now}
	h.mu.Unlock()

	var pushKey string
	if h.cfg.Push != nil {
		pushKey = h.cfg.Push.VAPIDPublicKey
	}

	content := &polyBody{
		html:              html,
		endpoint:          r.URL.Path,
		session:           id,
		transport:         h.cfg.Mode,
		retryDelay:        h.cfg.RetryDelay,
		maxRetryDelay:     h.cfg.MaxRetryDelay,
		defaultDebounce:   h.cfg.DefaultDebounce,
		transitionTimeout: h.cfg.TransitionTimeout,
		worker:            h.cfg.Worker,
		pushKey:           pushKey,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Prevent session ID leakage via Referer header on external links.
	w.Header().Set("Referrer-Policy", "same-origin")
	if h.cfg.Layout != nil {
		h.cfg.Layout(content).Render(w)
	} else {
		content.Render(w)
	}
}

// serveSession upgrades the connection and runs the session event loop.
// It checks pools in priority order — disconnected first (so a
// reconnecting client recovers its state), then pending (the normal
// path after a page load), and finally creates a fresh session as a
// fallback for direct transport connections without a prior GET.
func (h *Handler[S]) serveSession(w http.ResponseWriter, r *http.Request, upgrade func(http.ResponseWriter, *http.Request) (Transport, error)) {
	transport, err := upgrade(w, r)
	if err != nil {
		http.Error(w, "connection upgrade failed", http.StatusInternalServerError)
		return
	}

	// Close the transport if we exit before handing it to a session
	// event loop (e.g. a panic in InitialState or Render). Once
	// sess.run() starts, the event loop owns the transport lifecycle.
	running := false
	defer func() {
		if !running {
			transport.Close()
		}
	}()

	// Start keep-alive writes for transports that need them (SSE).
	// WebSocket has its own ping/pong and does not implement this.
	if hb, ok := transport.(Heartbeater); ok && h.cfg.HeartbeatInterval > 0 {
		hb.StartHeartbeat(h.cfg.HeartbeatInterval)
	}

	id := r.URL.Query().Get("session")

	// Try to reattach to a disconnected session.
	h.mu.Lock()
	if sess, ok := h.disconnected[id]; ok {
		delete(h.disconnected, id)
		h.active[id] = sess
		h.mu.Unlock()

		running = true
		h.reattach(sess, transport)
		return
	}
	h.mu.Unlock()

	// Try to claim a pending (pre-warmed) session.
	var state S
	var differ *jit.Differ

	h.mu.Lock()
	if ps, ok := h.pending[id]; ok {
		state = ps.state
		differ = ps.differ
		delete(h.pending, id)
	}
	h.mu.Unlock()

	if differ == nil {
		// This path handles direct transport connections without a prior
		// GET (e.g. bogus or missing session ID). Enforce MaxSessions
		// here too, otherwise this path bypasses the limit. Disconnected
		// sessions are excluded for the same reason as serveInitialPage.
		if h.cfg.MaxSessions > 0 {
			h.mu.Lock()
			full := len(h.pending)+len(h.active) >= h.cfg.MaxSessions
			h.mu.Unlock()
			if full {
				return
			}
		}

		id = newID()
		state = h.cfg.InitialState(r)
		if h.cfg.HandleParams != nil {
			state = h.cfg.HandleParams(state, Params{
				Path:  r.URL.Path,
				Query: r.URL.Query(),
			})
		}
		differ = jit.NewDiffer()

		tree := h.cfg.Render(state)
		differ.Render(tree)
	}

	now := time.Now()
	sess := &Session[S]{
		id:           id,
		state:        state,
		render:       h.cfg.Render,
		handle:       h.cfg.Handle,
		handleParams: h.cfg.HandleParams,
		differ:       differ,
		transport:    transport,
		logger:       h.cfg.Logger,
		createdAt:    now,
		lastActivity: now,
	}

	if h.cfg.Equal != nil {
		sess.equal = h.cfg.Equal
	}
	if h.cfg.OnStructuralChange != nil {
		sess.onStructuralChange = h.cfg.OnStructuralChange
	}

	h.mu.Lock()
	h.active[id] = sess
	h.mu.Unlock()

	h.wireDisconnect(sess)

	if h.cfg.OnConnect != nil {
		h.cfg.OnConnect(sess)
	}

	running = true
	sess.run()
}

// reattach reconnects a disconnected session with a new transport.
// A full re-render is sent because the client's DOM may have diverged
// while disconnected.
func (h *Handler[S]) reattach(sess *Session[S], transport Transport) {
	// The old transport's heartbeat goroutine stopped when it closed.
	// Start a fresh one for the new transport.
	if hb, ok := transport.(Heartbeater); ok && h.cfg.HeartbeatInterval > 0 {
		hb.StartHeartbeat(h.cfg.HeartbeatInterval)
	}

	sess.mu.Lock()
	sess.transport = transport
	sess.lastActivity = time.Now()
	sess.disconnectedAt = time.Time{}

	// Re-render current state and send to the client so it catches up.
	tree := sess.render(sess.state)
	html := sess.differ.Render(tree)
	sess.mu.Unlock()

	update := Update{
		Morphs: []Morph{{Key: "", HTML: html}},
	}
	if err := transport.SendUpdate(update); err != nil {
		sess.logger.Error("reattach send error", "session", sess.id, "err", err)
	}

	h.wireDisconnect(sess)
	sess.run()
}

// wireDisconnect installs the callback that moves a session into the
// disconnected pool (when reconnection is enabled) or removes it
// entirely. Must be called each time a transport is attached because
// the callback captures the handler's pool references.
func (h *Handler[S]) wireDisconnect(sess *Session[S]) {
	sess.onDisconnect = func() {
		// Write disconnectedAt under sess.mu before acquiring h.mu
		// to avoid nesting sess.mu inside h.mu. The reaper also
		// reads disconnectedAt under sess.mu (after releasing h.mu),
		// so this keeps the lock ordering consistent and prevents
		// a latent deadlock if any future code path acquires the
		// locks in the opposite order.
		if h.cfg.ReconnectTimeout > 0 {
			sess.mu.Lock()
			sess.disconnectedAt = time.Now()
			sess.mu.Unlock()
		}

		h.mu.Lock()
		delete(h.active, sess.id)
		if h.cfg.ReconnectTimeout > 0 {
			h.disconnected[sess.id] = sess
		}
		h.mu.Unlock()

		if h.cfg.OnDisconnect != nil {
			h.cfg.OnDisconnect(sess)
		}
	}
}

// Shutdown closes all active sessions and stops the background reaper.
// It blocks until every session has exited or ctx is cancelled. Safe
// to call more than once.
func (h *Handler[S]) Shutdown(ctx context.Context) error {
	h.closeOnce.Do(func() { close(h.done) })

	h.mu.Lock()
	sessions := make([]*Session[S], 0, len(h.active))
	for _, sess := range h.active {
		sessions = append(sessions, sess)
	}
	// The reaper is stopped (done is closed), so nothing else will
	// clean up these maps. Clear them to release memory.
	clear(h.pending)
	clear(h.disconnected)
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Go(func() {
			sess.Close()
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reap runs in a background goroutine, enforcing the lifecycle limits
// set in Config. It exits when the done channel is closed by Shutdown.
//
// Each pool is scanned under its own lock acquisition so that a large
// active session map does not block new connections or reconnects while
// the reaper is checking idle/lifetime limits on other pools.
func (h *Handler[S]) reap() {
	ticker := time.NewTicker(h.cfg.ReaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
		}

		now := time.Now()

		h.reapPending(now)
		h.reapDisconnected(now)
		h.reapActive(now)
	}
}

// reapPending removes pre-warmed sessions whose browser never connected.
func (h *Handler[S]) reapPending(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for id, ps := range h.pending {
		if now.Sub(ps.createdAt) > h.cfg.PendingTimeout {
			delete(h.pending, id)
		}
	}
}

// reapDisconnected removes sessions whose reconnect window has elapsed.
// Uses the same snapshot-check-delete pattern as reapActive to avoid
// holding h.mu while acquiring session locks.
func (h *Handler[S]) reapDisconnected(now time.Time) {
	if h.cfg.ReconnectTimeout <= 0 {
		return
	}

	h.mu.Lock()
	checkList := make([]*Session[S], 0, len(h.disconnected))
	for _, sess := range h.disconnected {
		checkList = append(checkList, sess)
	}
	h.mu.Unlock()

	var expired []*Session[S]
	for _, sess := range checkList {
		sess.mu.Lock()
		past := now.Sub(sess.disconnectedAt) > h.cfg.ReconnectTimeout
		sess.mu.Unlock()
		if past {
			expired = append(expired, sess)
		}
	}

	if len(expired) > 0 {
		h.mu.Lock()
		for _, sess := range expired {
			if h.disconnected[sess.id] == sess {
				delete(h.disconnected, sess.id)
			}
		}
		h.mu.Unlock()
	}
}

// reapActive closes sessions that have exceeded their idle or lifetime
// limits. The work is split into three phases so the handler lock is
// never held while acquiring individual session locks:
//
//  1. Snapshot session pointers under h.mu (fast — no session locks).
//  2. Check each session's timestamps under sess.mu (no handler lock).
//  3. Re-acquire h.mu to delete expired entries, re-verifying each one
//     in case a reconnect claimed the session between phases.
func (h *Handler[S]) reapActive(now time.Time) {
	// Phase 1: snapshot pointers so we can release h.mu quickly.
	h.mu.Lock()
	checkList := make([]*Session[S], 0, len(h.active))
	for _, sess := range h.active {
		checkList = append(checkList, sess)
	}
	h.mu.Unlock()

	// Phase 2: check timestamps without holding the handler lock.
	var expired []*Session[S]
	for _, sess := range checkList {
		sess.mu.Lock()
		idle := h.cfg.IdleTimeout > 0 && now.Sub(sess.lastActivity) > h.cfg.IdleTimeout
		aged := h.cfg.MaxLifetime > 0 && now.Sub(sess.createdAt) > h.cfg.MaxLifetime
		sess.mu.Unlock()

		if idle || aged {
			expired = append(expired, sess)
		}
	}

	// Phase 3: delete from the active map and close transports.
	if len(expired) > 0 {
		h.mu.Lock()
		for _, sess := range expired {
			// Re-verify the session is still the same pointer in the
			// active pool. A reconnect between phase 1 and now could
			// have replaced it.
			if h.active[sess.id] == sess {
				delete(h.active, sess.id)
			}
		}
		h.mu.Unlock()

		var wg sync.WaitGroup
		for _, sess := range expired {
			wg.Go(func() {
				h.cfg.Logger.Info("closing session", "session", sess.ID())
				sess.Close()
			})
		}
		wg.Wait()
	}
}
