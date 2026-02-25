package poly

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// ServeHTTP implements http.Handler. A single endpoint serves all three
// request types: the initial HTML page (GET without upgrade headers),
// the transport connection (WebSocket upgrade or SSE stream), and POST
// events (SSE mode only). The Mode field in Config determines which
// transport paths are active. Requests that don't match any transport
// path fall through to the initial page render.
func (h *Handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Push subscription registrations arrive as POST with a special
	// header, regardless of transport mode. Handle them before the
	// mode switch to avoid being mistaken for an SSE event.
	if r.Method == "POST" && r.Header.Get("X-Poly-Push-Subscribe") == "true" {
		h.handlePushSubscribe(w, r)
		return
	}

	switch h.cfg.Mode {
	case SSEOnly:
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

	case WebSocketWithFallback:
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

	default: // WebSocketOnly
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
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(h.cfg.AllowedOrigins) > 0 {
		return slices.Contains(h.cfg.AllowedOrigins, origin)
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

	// The transport's PushEvent writes to a buffered channel that the
	// reader goroutine consumes — it does not touch session state.
	// The transport pointer is only modified by the loop (reattach),
	// and an active session always has a valid transport.
	pusher, ok := sess.transport.(EventPusher)
	if !ok {
		http.Error(w, "transport does not accept events", http.StatusMethodNotAllowed)
		return
	}

	// Cap the request body to prevent a malicious client from sending
	// a multi-gigabyte payload and exhausting server memory.
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxEventBytes)

	var ev Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}

	if err := pusher.PushEvent(ev); err != nil {
		if errors.Is(err, ErrEventBufferFull) {
			http.Error(w, "event buffer full", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "session closed", http.StatusGone)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxEventBytes)

	var sub PushSubscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}

	// Send the subscription to the session loop so it's stored
	// without racing with other loop operations. The select guards
	// against hanging if the session is destroyed mid-request.
	select {
	case sess.cmds <- func() { sess.pushSub = &sub }:
	case <-sess.ctx.Done():
		http.Error(w, "session closed", http.StatusGone)
		return
	}

	go h.cfg.Push.OnSubscribe(sess, sub)
	w.WriteHeader(http.StatusNoContent)
}
