package tether

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/push"
)

// ServeHTTP implements http.Handler. A single endpoint serves the
// initial HTML page (GET without upgrade headers), the transport
// connection (WebSocket upgrade or SSE stream), POST events (SSE mode
// only), and the embedded client JS runtime at /_tether/. The Mode field
// in StatefulConfig determines which transport paths are active. Requests that
// don't match any transport path fall through to the initial page render.
func (h *Handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The dev dashboard shows session pool counts and the recent
	// diagnostic stream. Dev mode only - Health() serves production.
	if r.URL.Path == "/_tether/debug" && h.debugLog != nil {
		h.serveDebug(w, r)
		return
	}

	// Serve the embedded client runtime (JS, idiomorph, service worker).
	if strings.HasPrefix(r.URL.Path, "/_tether/") {
		http.StripPrefix("/_tether", h.clientHandler).ServeHTTP(w, r)
		return
	}

	// Serve embedded application assets at their configured prefixes.
	for _, m := range h.assetMounts {
		if strings.HasPrefix(r.URL.Path, m.prefix) {
			m.handler.ServeHTTP(w, r)
			return
		}
	}

	// Framework verbs share the page endpoint, selected by ?tether=.
	// Both are POST-only and CSRF-checked - they act on session state,
	// so a cross-site GET (e.g. <img src="...?tether=destroy">) must
	// not be able to trigger them.
	switch r.URL.Query().Get("tether") {
	case "ticket":
		h.handleConnectTicket(w, r)
		return
	case "destroy":
		h.handleDestroyBeacon(w, r)
		return
	}

	// File uploads arrive as multipart POST with an Tether-Upload
	// header. Handle them before the mode switch so they work with
	// all transport modes.
	if r.Method == "POST" && r.Header.Get("Tether-Upload") != "" {
		dev.Debug("upload received", "session", r.Header.Get("Tether-Session"), "path", r.URL.Path, "remote", r.RemoteAddr)
		h.handleUpload(w, r)
		return
	}

	// Push subscription registrations arrive as POST with a special
	// header, regardless of transport mode. Handle them before the
	// mode switch to avoid being mistaken for an SSE event.
	if r.Method == "POST" && r.Header.Get("Tether-Push-Subscribe") == "true" {
		dev.Debug("push subscription received", "session", r.Header.Get("Tether-Session"))
		h.handlePushSubscribe(w, r)
		return
	}

	switch h.cfg.Mode {
	case mode.ServerSentEvents:
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			// SSE stream is a read-only GET - safe method, no origin
			// check needed. POST events have their own check.
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	case mode.Both:
		if r.Header.Get("Upgrade") == "websocket" {
			if !h.wsOriginAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	default: // mode.WebSocket
		if r.Header.Get("Upgrade") == "websocket" {
			if !h.wsOriginAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
	}

	h.serveInitialPage(w, r)
}

// wsOriginAllowed checks the Origin header on WebSocket upgrade
// requests to prevent cross-site WebSocket hijacking. Unlike standard
// HTTP requests, WebSocket connections are not protected by the
// browser's Same-Origin Policy - the server must validate the Origin
// during the handshake.
//
// The check mirrors the logic in [http.CrossOriginProtection] but
// without the safe-method bypass: Sec-Fetch-Site is checked first
// (available in all browsers since 2023), then Origin is compared
// against TrustedOrigins or the Host header as a fallback.
//
// Requests without Sec-Fetch-Site or Origin headers are allowed -
// they come from same-origin navigations or non-browser clients.
func (h *Handler[S]) wsOriginAllowed(r *http.Request) bool {
	// Sec-Fetch-Site is the primary signal. Modern browsers send it
	// on all requests including WebSocket upgrades.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// Header absent - fall through to Origin check.
	default:
		// "cross-site", "same-site", or any other value.
		// Check if the origin is explicitly trusted.
		origin := r.Header.Get("Origin")
		return slices.Contains(h.app.Security.TrustedOrigins, origin)
	}

	// No Sec-Fetch-Site header. Check the Origin header.
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Neither header present - same-origin or non-browser client.
		return true
	}

	// If TrustedOrigins is configured, match exactly.
	if len(h.app.Security.TrustedOrigins) > 0 {
		return slices.Contains(h.app.Security.TrustedOrigins, origin)
	}

	// No TrustedOrigins - compare Origin's host:port against the
	// Host header. This matches the stdlib's fallback behaviour
	// (see net/http/csrf.go line 161).
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// handleConnectTicket issues a one-time connect ticket. The client
// POSTs here immediately before opening a transport, carrying its
// session ID (and any session it replaces after a page refresh) in
// headers so neither appears in a URL. The response body is the
// opaque ticket the transport connects with. See ticket.go.
func (h *Handler[S]) handleConnectTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.csrf.Check(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	// Both IDs are optional - a first connect has no session yet -
	// but when present they must look like IDs this framework issued.
	id := r.Header.Get("Tether-Session")
	if id != "" && !validSessionID(id) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	replaces := r.Header.Get("Tether-Replaces")
	if replaces != "" && !validSessionID(replaces) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}

	tok, ok := h.issueTicket(id, replaces, r.UserAgent(), time.Now())
	if !ok {
		http.Error(w, "too many pending connections", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := io.WriteString(w, tok); err != nil {
		// The client vanished before it could read the ticket. It
		// expires on its own, so there is nothing to undo.
		dev.Debug("connect ticket write failed", "error", err)
	}
}

// handleDestroyBeacon destroys a session the client knows is
// abandoned - navigator.sendBeacon fires it on page unload so the
// session doesn't sit out its disconnect timer. sendBeacon always
// POSTs; the session ID travels in the body, keeping it out of URLs.
func (h *Handler[S]) handleDestroyBeacon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.csrf.Check(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256))
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(string(body))
	if !validSessionID(id) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	h.destroyByID(id)
	w.WriteHeader(http.StatusNoContent)
}

// handlePostEvent receives a client event via HTTP POST. This is the
// client→server path for SSE mode, where the EventSource connection
// is unidirectional. WebSocket transports receive events on the
// socket itself and do not use this path.
//
// The event is enqueued directly on the session's command channel
// rather than routed through the transport. This avoids reading the
// transport pointer from outside the loop goroutine, eliminating a
// data race during reconnection when the transport is swapped.
func (h *Handler[S]) handlePostEvent(w http.ResponseWriter, r *http.Request) {
	if err := h.csrf.Check(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	// The session ID is sent as a header rather than a query parameter
	// to keep it out of server access logs and browser history.
	id := r.Header.Get("Tether-Session")
	if !validSessionID(id) {
		http.Error(w, "missing or invalid Tether-Session header", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	sess, ok := h.active[id]
	h.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Cap the request body to prevent a malicious client from sending
	// a multi-gigabyte payload and exhausting server memory.
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.Limits.MaxEventBytes)

	var ev Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "malformed JSON event", http.StatusBadRequest)
		return
	}

	dev.Debug("POST event", "session", id, "action", ev.Action, "type", ev.Type, "path", r.URL.Path, "remote", r.RemoteAddr)

	// Non-blocking send: if the buffer has room the event is accepted
	// immediately. If not, check whether the session is closing (410)
	// or simply overloaded (429).
	select {
	case sess.cmds <- func() { sess.exec(ev) }:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	default:
		if sess.ctx.Err() != nil {
			http.Error(w, "session closed", http.StatusGone)
			return
		}
		http.Error(w, "event buffer full", http.StatusTooManyRequests)
	}
}

// handlePushSubscribe receives a push subscription from the client JS
// after a successful PushManager.subscribe() call. The subscription is
// delivered to the PushConfig.OnSubscribe callback along with the
// session that sent it.
func (h *Handler[S]) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Push == nil || h.cfg.Push.OnSubscribe == nil {
		http.Error(w, "push not configured", http.StatusNotFound)
		return
	}
	if err := h.csrf.Check(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	id := r.Header.Get("Tether-Session")
	if !validSessionID(id) {
		http.Error(w, "missing or invalid Tether-Session header", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	sess, ok := h.active[id]
	h.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.Limits.MaxPushSubscriptionBytes)

	var sub push.Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "malformed JSON subscription", http.StatusBadRequest)
		return
	}
	if err := sub.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Non-blocking send: store the subscription via the session loop
	// so it doesn't race with other loop operations. If the buffer is
	// full, the session is overloaded - return 429 to apply back-pressure
	// (matching the pattern in handlePostEvent).
	select {
	case sess.cmds <- func() { sess.pushSub.Store(&sub) }:
	default:
		if sess.ctx.Err() != nil {
			http.Error(w, "session closed", http.StatusGone)
			return
		}
		http.Error(w, "session busy", http.StatusTooManyRequests)
		return
	}

	// Fire OnSubscribe asynchronously so the HTTP response returns
	// immediately - the callback receives the subscription as a
	// parameter so it doesn't need to read session state. Panic
	// recovery matches the pattern used in exec() and runCmd().
	go func() {
		defer func() {
			if r := recover(); r != nil {
				err := panicErr(r)
				dev.Log().Error("panic in OnSubscribe", "session", sess.ID(), "panic", r)
				sess.emitDiagnostic(Diagnostic{
					Kind:      HandlerPanic,
					SessionID: sess.ID(),
					Err:       err,
					Detail:    "OnSubscribe",
				})
			}
		}()
		h.cfg.Push.OnSubscribe(sess.ctx, sess, sub)
	}()

	w.WriteHeader(http.StatusNoContent)
}
