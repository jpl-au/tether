package tether

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// pendingSession holds a pre-warmed session created during the initial GET
// request. The state and differ are seeded so that the WebSocket can attach
// without repeating the initial render. Effects captured while running
// OnNavigate during the GET (SetTitle, Toast, Announce - a Navigate
// redirect is answered with a real 302 instead) are carried here and
// delivered in the first update after the transport connects.
type pendingSession[S any] struct {
	state     S
	differ    *jit.Differ
	createdAt time.Time
	userAgent string
	effects   Effects
}

// defaultPendingTimeout is used when PendingTimeout is zero.
const defaultPendingTimeout = 30 * time.Second

// Handler manages the lifecycle of tether sessions. Sessions move through
// three pools - pending, active, and disconnected - so the server can
// pre-warm state on the initial GET and preserve it across brief network
// interruptions. Use Shutdown for graceful termination.
//
// The handler also serves the embedded client runtime at /_tether/ - there
// is no need to mount a separate file server for the JS assets.
type Handler[S any] struct {
	app          App
	cfg          StatefulConfig[S]
	mu           sync.RWMutex
	pending      map[string]*pendingSession[S]
	active       map[string]*StatefulSession[S]
	disconnected map[string]*StatefulSession[S]
	done         chan struct{}
	closeOnce    sync.Once
	shutdownDone chan struct{}
	draining     atomic.Bool
	drainNotify  chan struct{} // buffered(1), signalled when pools empty during drain
	uploadWG     sync.WaitGroup
	// Reservations include requests still running application initialisers
	// and pending sessions being claimed, before they enter a pool.
	creating        int
	pendingCreating int
	connecting      map[string]chan struct{}

	// tickets holds outstanding one-time connect tickets, keyed by
	// token. See ticket.go for why transports connect with a ticket
	// instead of the session ID.
	ticketMu sync.Mutex
	tickets  map[string]connectTicket

	// debugLog records recent diagnostics for the dev dashboard at
	// /_tether/debug. Nil outside dev mode.
	debugLog *diagnosticLog

	// csrf checks cross-origin requests using Go 1.25's standard
	// library CrossOriginProtection. Safe methods (GET, HEAD) are
	// always allowed; non-safe methods are checked against
	// Sec-Fetch-Site and Origin headers.
	csrf *http.CrossOriginProtection

	// clientHandler serves the embedded JS runtime at /_tether/*.
	clientHandler http.Handler

	// wireFormat is the resolved wire format for this handler,
	// combining App.WireFormat and StatefulConfig.WireFormat.
	wireFormat wire.Format

	// encoder serialises updates using wireFormat. All sessions
	// inherit this encoder.
	encoder wire.Encoder

	// assetMounts serves embedded application assets at their
	// configured URL prefixes, one per [Asset] in App.Assets.
	assetMounts []assetMount

	// Diagnostics emits framework-level events so application code
	// can observe them for metrics, alerting, or custom logging.
	// The framework is quiet by default - slog is only used for
	// panics. All other operational signals (transport errors,
	// encode failures, buffer overflows, upload errors) flow
	// exclusively through this bus.
	//
	// Subscribe with [Bus.Subscribe] (synchronous) or
	// [Bus.SubscribeAsync] (own goroutine per event, safe for I/O):
	//
	//	h.Diagnostics.Subscribe(ctx, func(d tether.Diagnostic) {
	//	    metrics.Inc("tether." + string(d.Kind))
	//	})
	//
	//	h.Diagnostics.SubscribeAsync(ctx, func(d tether.Diagnostic) {
	//	    if d.Kind == tether.HandlerPanic {
	//	        alerting.Critical(d.SessionID, d.Err)
	//	    }
	//	})
	Diagnostics *Bus[Diagnostic]
}

// reserveCapacity makes the limit check and reservation atomic without
// holding the pool lock while application callbacks or rendering run.
func (h *Handler[S]) reserveCapacity(pending bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.done:
		return false
	default:
	}
	if h.draining.Load() {
		return false
	}
	if pending && h.app.MaxPending > 0 && len(h.pending)+h.pendingCreating >= h.app.MaxPending {
		return false
	}
	if h.app.MaxSessions > 0 && len(h.pending)+len(h.active)+len(h.disconnected)+h.creating >= h.app.MaxSessions {
		return false
	}
	h.creating++
	if pending {
		h.pendingCreating++
	}
	return true
}

func (h *Handler[S]) releaseCapacity(pending bool) {
	h.mu.Lock()
	h.creating--
	if pending {
		h.pendingCreating--
	}
	h.notifyDrain()
	h.mu.Unlock()
}

// claimConnection excludes overlapping initialisation for the same ID.
// Release once its reader is installed; later requests can then take over.
func (h *Handler[S]) claimConnection(id string) (func(), bool) {
	if id == "" {
		return func() {}, true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connecting[id] != nil {
		return nil, false
	}
	if h.connecting == nil {
		h.connecting = make(map[string]chan struct{})
	}
	token := make(chan struct{})
	h.connecting[id] = token
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.connecting, id)
			h.mu.Unlock()
		})
	}, true
}

// assetMount pairs a URL prefix with a handler that serves files from
// the corresponding [Asset] filesystem.
type assetMount struct {
	prefix  string
	handler http.Handler
}

// destroySession performs permanent cleanup for a session that is no
// longer reachable (reaped, shutdown, or disconnected with timeout -1).
// Cancelling the context causes the session loop to exit.
func (h *Handler[S]) destroySession(s *StatefulSession[S]) {
	h.releaseSession(s, false)
}

// Startup callbacks can outlive destruction. A late automatic join must
// not put the retired session back into a group after cleanup removed it.
func (h *Handler[S]) joinGroups(s *StatefulSession[S]) {
	if s.ctx.Err() != nil {
		return
	}
	for _, g := range h.cfg.Groups {
		g.Add(s)
		if s.ctx.Err() != nil {
			g.Remove(s)
		}
	}
}

// clearRecovery runs on the loop after catch-up. Shutdown's final save
// waits for this deletion, so late startup callbacks cannot erase it.
func (h *Handler[S]) clearRecovery(s *StatefulSession[S]) {
	if h.cfg.DiffStore != nil {
		if err := h.cfg.DiffStore.Delete(s.ctx, s.id); err != nil {
			s.emitDiagnostic(Diagnostic{Kind: StoreError, SessionID: s.id, Err: err, Detail: "delete"})
		}
	}
	if h.cfg.SessionStore != nil {
		if err := h.cfg.SessionStore.Delete(s.ctx, s.id); err != nil {
			s.emitDiagnostic(Diagnostic{Kind: SessionStoreError, SessionID: s.id, Err: err, Detail: "delete"})
		}
	}
}

// releaseSession optionally preserves application state for recovery after
// shutdown. Diff snapshots and group membership are always released.
func (h *Handler[S]) releaseSession(s *StatefulSession[S], preserveState bool) {
	s.lifecycleMu.Lock()
	if s.stop != nil {
		s.stop()
	}

	// For frozen sessions the loop already exited and cleanup()
	// skipped closing destroyed. Move them straight to Destroyed and
	// close the channel so Shutdown waiters are unblocked. The CAS
	// loses harmlessly if a concurrent thaw claimed the stub first;
	// destroyedOnce ensures a single close either way.
	s.status.CompareAndSwap(int32(Frozen), int32(Destroyed))
	s.destroyedOnce.Do(func() { close(s.destroyed) })
	loopDone := s.loopDone
	s.lifecycleMu.Unlock()
	// Freeze leaves lifecycle timers armed after its loop exits. A
	// permanent destroy must stop them too, after loop-owned writes finish.
	go func() {
		<-loopDone
		if s.disconnectTimer != nil {
			s.disconnectTimer.Stop()
		}
		if s.lifetimeTimer != nil {
			s.lifetimeTimer.Stop()
		}
	}()

	// Remove stored data for sessions that were offloaded during
	// disconnect. No-op if nothing was stored.
	if h.cfg.DiffStore != nil {
		if err := h.cfg.DiffStore.Delete(context.Background(), s.id); err != nil {
			dev.Warn("store delete failed on destroy", "session", s.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      StoreError,
				SessionID: s.id,
				Err:       err,
				Detail:    "delete",
			})
		}
	}
	if h.cfg.SessionStore != nil && !preserveState {
		if err := h.cfg.SessionStore.Delete(context.Background(), s.id); err != nil {
			dev.Warn("session store delete failed on destroy", "session", s.id, "error", err)
			h.Diagnostics.Publish(Diagnostic{
				Kind:      SessionStoreError,
				SessionID: s.id,
				Err:       err,
				Detail:    "delete",
			})
		}
	}

	for _, g := range h.cfg.Groups {
		g.Remove(s)
	}
}

// destroyByID checks client binding before destroying a session by ID.
// It returns false on a binding mismatch; an absent session is a no-op.
// Used by the session handoff (replaces) and the pagehide beacon
// (destroy) to skip the disconnect timer when the client knows the
// session is abandoned. Checks the active pool as well as the
// disconnected pool - on a fast page refresh the new connection can
// arrive before the old transport has finished closing, leaving the
// replaced session still in h.active.
func (h *Handler[S]) destroyByID(id, userAgent string) bool {
	h.mu.RLock()
	sess := h.disconnected[id]
	if sess == nil {
		sess = h.active[id]
	}
	h.mu.RUnlock()
	if sess == nil {
		return true
	}
	// The application matcher may call back into the handler.
	if !h.app.Security.matchUA(sess.userAgent, userAgent) {
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionBindingFailed,
			SessionID: id,
			Detail:    "user-agent mismatch on destroy",
		})
		return false
	}

	h.mu.Lock()
	// The session may have moved pools during matching. Remove only the
	// instance we validated, leaving any replacement with the same ID alone.
	if h.disconnected[id] == sess {
		delete(h.disconnected, id)
	} else if h.active[id] == sess {
		delete(h.active, id)
	} else {
		h.mu.Unlock()
		return true
	}
	h.notifyDrain()
	h.mu.Unlock()
	dev.Debug("session replaced", "session", id, "endpoint", sess.endpoint)
	h.destroySession(sess)
	if h.cfg.OnDisconnect != nil {
		h.cfg.OnDisconnect(sess)
	}
	return true
}

// sessionDestroyed is the convergence point for teardown initiated
// inside the session (panic, idle timeout, MaxLifetime, explicit
// stop). Called from cleanup on the loop goroutine, it removes the
// session from whichever pool still holds it and runs the permanent
// cleanup. Idempotent: teardown that came through the transport side
// (disconnect timer, destroy beacon, shutdown) already removed the
// session from the pools and ran destroySession, so this becomes a
// no-op.
func (h *Handler[S]) sessionDestroyed(s *StatefulSession[S]) {
	h.mu.Lock()
	inActive := h.active[s.id] == s
	inDisconnected := h.disconnected[s.id] == s
	tracked := inActive || inDisconnected
	if tracked {
		delete(h.active, s.id)
		delete(h.disconnected, s.id)
		h.notifyDrain()
	}
	h.mu.Unlock()
	if !tracked {
		return
	}

	h.destroySession(s)
	if h.cfg.OnDisconnect != nil {
		h.cfg.OnDisconnect(s)
	}
}
