package tether

import (
	"time"
)

// The session ID is a bearer token: possession (plus a matching
// User-Agent) is enough to reattach to a live session. It therefore
// never travels in a URL, where web-server access logs, reverse
// proxies, and APM traces would capture it. POST paths carry it in
// the Tether-Session header, but the transport connect (WebSocket
// upgrade, EventSource stream) is a GET whose only client-controlled
// channel shared by both transports is the URL.
//
// Connect tickets bridge that gap. Before connecting, the client
// POSTs to the endpoint with ?tether=ticket, carrying the session ID
// (and any replaced session) in headers. The server answers with an
// opaque, single-use, short-lived ticket. The transport then connects
// with ?ticket=<token> - the only secret in the URL is one that
// expires in seconds and dies on first use, so a logged copy is
// worthless.

// connectTicketTTL is how long an issued ticket remains redeemable.
// It only needs to cover the gap between the ticket POST returning
// and the transport connecting - normally milliseconds. Generous
// enough for a congested mobile link, short enough that a leaked
// token is useless.
const connectTicketTTL = 30 * time.Second

// connectTicket carries the connect parameters from the ticket POST
// to the transport connect.
type connectTicket struct {
	session   string
	replaces  string
	userAgent string
	expires   time.Time
}

// issueTicket stores the connect parameters under a fresh random
// token and returns it. Returns false when the ticket table is full -
// outstanding tickets are capped by App.MaxPending, since like
// pending sessions they are cheap, unauthenticated server state.
func (h *Handler[S]) issueTicket(session, replaces, userAgent string, now time.Time) (string, bool) {
	h.ticketMu.Lock()
	defer h.ticketMu.Unlock()

	if h.tickets == nil {
		h.tickets = make(map[string]connectTicket)
	}
	// Lazily drop expired tickets so abandoned connects (issued but
	// never redeemed) cannot accumulate.
	for tok, t := range h.tickets {
		if now.After(t.expires) {
			delete(h.tickets, tok)
		}
	}
	if h.app.MaxPending > 0 && len(h.tickets) >= h.app.MaxPending {
		return "", false
	}

	tok := newID()
	h.tickets[tok] = connectTicket{
		session:   session,
		replaces:  replaces,
		userAgent: userAgent,
		expires:   now.Add(connectTicketTTL),
	}
	return tok, true
}

// redeemTicket consumes a ticket exactly once. The redeeming request's
// User-Agent must match the issuing request's - a mismatch means the
// token was captured in transit or from a log and replayed elsewhere.
func (h *Handler[S]) redeemTicket(tok, userAgent string) (connectTicket, bool) {
	h.ticketMu.Lock()
	t, ok := h.tickets[tok]
	if ok {
		delete(h.tickets, tok)
	}
	h.ticketMu.Unlock()

	if !ok || time.Now().After(t.expires) {
		return connectTicket{}, false
	}
	if !h.app.Security.matchUA(t.userAgent, userAgent) {
		h.Diagnostics.Publish(Diagnostic{
			Kind:      SessionBindingFailed,
			SessionID: t.session,
			Detail:    "user-agent mismatch on ticket redemption",
		})
		return connectTicket{}, false
	}
	return t, true
}

// validSessionID reports whether id is shaped like an identifier this
// framework could have issued. Session IDs come from crypto/rand.Text
// (base32: A-Z and 2-7); hyphen and underscore are also accepted so
// base64url-style IDs from custom tooling keep working. The check is
// about charset safety, not entropy - it stops path separators, dots,
// and percent escapes before a client-supplied ID reaches a pool
// lookup or, critically, a store implementation that might use it as
// a filename or key.
func validSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}
