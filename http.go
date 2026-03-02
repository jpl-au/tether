package poly

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/jpl-au/fluent-poly/mode"
	"github.com/jpl-au/fluent-poly/push"
)

// ServeHTTP implements http.Handler. A single endpoint serves the
// initial HTML page (GET without upgrade headers), the transport
// connection (WebSocket upgrade or SSE stream), POST events (SSE mode
// only), and the embedded client JS runtime at /_poly/. The Mode field
// in Config determines which transport paths are active. Requests that
// don't match any transport path fall through to the initial page render.
func (h *Handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Serve the embedded client runtime (JS, idiomorph, service worker).
	if strings.HasPrefix(r.URL.Path, "/_poly/") {
		http.StripPrefix("/_poly", h.clientHandler).ServeHTTP(w, r)
		return
	}

	// Serve embedded application assets at their configured prefixes.
	for _, m := range h.assetMounts {
		if strings.HasPrefix(r.URL.Path, m.prefix) {
			m.handler.ServeHTTP(w, r)
			return
		}
	}

	// File uploads arrive as multipart POST with an X-Poly-Upload
	// header. Handle them before the mode switch so they work with
	// all transport modes.
	if r.Method == "POST" && r.Header.Get("X-Poly-Upload") != "" {
		slog.Debug("upload received", "session", r.Header.Get("X-Poly-Session"), "path", r.URL.Path, "remote", r.RemoteAddr)
		h.handleUpload(w, r)
		return
	}

	// Push subscription registrations arrive as POST with a special
	// header, regardless of transport mode. Handle them before the
	// mode switch to avoid being mistaken for an SSE event.
	if r.Method == "POST" && r.Header.Get("X-Poly-Push-Subscribe") == "true" {
		slog.Debug("push subscription received", "session", r.Header.Get("X-Poly-Session"))
		h.handlePushSubscribe(w, r)
		return
	}

	switch h.cfg.Mode {
	case mode.ServerSentEvents:
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	case mode.Both:
		if r.Header.Get("Upgrade") == "websocket" {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Fallback)
			return
		}
		if r.Method == "POST" {
			h.handlePostEvent(w, r)
			return
		}

	default: // mode.WebSocket
		if r.Header.Get("Upgrade") == "websocket" {
			if !h.originAllowed(r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			h.serveSession(w, r, h.cfg.Upgrade)
			return
		}
	}

	h.serveInitialPage(w, r)
}

// originAllowed checks the request's Origin header against
// Config.AllowedOrigins. When AllowedOrigins is configured, the Origin
// must match one of the listed values exactly. When AllowedOrigins is
// empty, it falls back to same-host checking as basic CSRF protection.
// Requests without an Origin header (e.g. same-origin navigations or
// non-browser clients) are always allowed.
func (h *Handler[S]) originAllowed(r *http.Request) bool {
	return checkOrigin(r, h.cfg.Security.AllowedOrigins)
}

// checkOrigin is the shared origin-checking logic used by both
// [Handler] and [pageHandler]. When allowedOrigins is non-empty the
// Origin must match exactly; otherwise a same-host check is applied.
//
// Requests without an Origin header are allowed because all
// state-changing paths (POST events, uploads, push subscriptions)
// require custom headers (X-Poly-Session, X-Poly-Upload, etc.) that
// trigger a CORS preflight — browsers never send a cross-origin
// request with custom headers without a successful preflight first.
// This means a missing Origin only occurs for same-origin requests
// and non-browser clients, both of which are safe.
func checkOrigin(r *http.Request, allowedOrigins []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(allowedOrigins) > 0 {
		return slices.Contains(allowedOrigins, origin)
	}
	// No AllowedOrigins configured — fall back to same-host check
	// as basic CSRF protection. Compare hostnames only so that
	// Origin: http://localhost matches Host: localhost:8080.
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return stripPort(u.Host) == stripPort(r.Host)
}

// stripPort returns the host portion of a host:port string. If there
// is no port, the input is returned unchanged.
func stripPort(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return host
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
	if !h.originAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	// The session ID is sent as a header rather than a query parameter
	// to keep it out of server access logs and browser history.
	id := r.Header.Get("X-Poly-Session")
	if id == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	sess, ok := h.active[id]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Cap the request body to prevent a malicious client from sending
	// a multi-gigabyte payload and exhausting server memory.
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.Limits.MaxEventBytes)

	var ev Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}

	slog.Debug("POST event", "session", id, "action", ev.Action, "type", ev.Type, "path", r.URL.Path, "remote", r.RemoteAddr)

	// Non-blocking send: if the buffer has room the event is accepted
	// immediately. If not, check whether the session is closing (410)
	// or simply overloaded (429).
	select {
	case sess.cmds <- func() { sess.exec(ev) }:
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
	if !h.originAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	id := r.Header.Get("X-Poly-Session")
	if id == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	sess, ok := h.active[id]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.Limits.MaxEventBytes)

	var sub push.Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}

	// Send the subscription to the session loop so it's stored
	// without racing with other loop operations. The select guards
	// against hanging if the session is destroyed mid-request.
	select {
	case sess.cmds <- func() { sess.pushSub.Store(&sub) }:
	case <-sess.ctx.Done():
		http.Error(w, "session closed", http.StatusGone)
		return
	}

	go h.cfg.Push.OnSubscribe(sess, sub)
	w.WriteHeader(http.StatusNoContent)
}
