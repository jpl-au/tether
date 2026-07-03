package tether

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"sync"
	"time"
)

// The dev dashboard makes the session accounting visible: pool
// counts, outstanding connect tickets, and the most recent
// diagnostics, on an auto-refreshing page at /_tether/debug. It is
// served only in dev mode - the pool counts are also available
// programmatically via [Handler.Health] for production monitoring.

// debugLogCapacity bounds the in-memory diagnostic ring buffer.
const debugLogCapacity = 100

// loggedDiagnostic is a diagnostic stamped with its arrival time for
// display on the dashboard.
type loggedDiagnostic struct {
	at time.Time
	d  Diagnostic
}

// diagnosticLog keeps the most recent diagnostics in a ring buffer.
// Created only in dev mode, so production pays nothing for it.
type diagnosticLog struct {
	mu      sync.Mutex
	entries []loggedDiagnostic
	next    int
	total   int
}

// newDiagnosticLog subscribes to the bus and records every event.
// The subscription lives for the process - the dashboard is a dev
// tool tied to the handler's lifetime.
func newDiagnosticLog(bus *Bus[Diagnostic]) *diagnosticLog {
	l := &diagnosticLog{entries: make([]loggedDiagnostic, debugLogCapacity)}
	bus.Subscribe(context.Background(), func(d Diagnostic) {
		l.mu.Lock()
		l.entries[l.next] = loggedDiagnostic{at: time.Now(), d: d}
		l.next = (l.next + 1) % len(l.entries)
		l.total++
		l.mu.Unlock()
	})
	return l
}

// recent returns the logged diagnostics newest-first, plus the
// all-time count (which keeps climbing after the ring wraps).
func (l *diagnosticLog) recent() ([]loggedDiagnostic, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]loggedDiagnostic, 0, len(l.entries))
	for i := 1; i <= len(l.entries); i++ {
		e := l.entries[(l.next-i+len(l.entries))%len(l.entries)]
		if e.at.IsZero() {
			break
		}
		out = append(out, e)
	}
	return out, l.total
}

// serveDebug renders the dev dashboard. Only reachable in dev mode
// (the route is gated in ServeHTTP).
func (h *Handler[S]) serveDebug(w http.ResponseWriter, r *http.Request) {
	hs := h.Health()
	h.ticketMu.Lock()
	tickets := len(h.tickets)
	h.ticketMu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	fmt.Fprint(w, `<!doctype html><meta charset="utf-8">`)
	fmt.Fprint(w, `<meta http-equiv="refresh" content="2">`)
	fmt.Fprint(w, `<title>tether debug</title>`)
	fmt.Fprint(w, `<style>
body{font:14px/1.5 ui-monospace,monospace;margin:2rem;color:#111}
h1{font-size:1.1rem} h2{font-size:1rem;margin-top:1.5rem}
table{border-collapse:collapse;width:100%}
td,th{text-align:left;padding:.2rem .8rem .2rem 0;border-bottom:1px solid #eee;vertical-align:top}
.n{font-weight:700} .kind{white-space:nowrap}
@media (prefers-color-scheme:dark){body{background:#111;color:#ddd}td,th{border-color:#333}}
</style>`)
	fmt.Fprint(w, `<h1>tether debug</h1>`)

	fmt.Fprint(w, `<h2>Sessions</h2><table>`)
	fmt.Fprintf(w, `<tr><th>pending</th><th>active</th><th>disconnected</th><th>connect tickets</th></tr>`)
	fmt.Fprintf(w, `<tr><td class="n">%d</td><td class="n">%d</td><td class="n">%d</td><td class="n">%d</td></tr>`,
		hs.Pending, hs.Active, hs.Disconnected, tickets)
	fmt.Fprint(w, `</table>`)

	entries, total := h.debugLog.recent()
	fmt.Fprintf(w, `<h2>Diagnostics (%d shown, %d total)</h2>`, len(entries), total)
	fmt.Fprint(w, `<table><tr><th>time</th><th>kind</th><th>session</th><th>detail</th><th>error</th></tr>`)
	for _, e := range entries {
		errText := ""
		if e.d.Err != nil {
			errText = e.d.Err.Error()
		}
		fmt.Fprintf(w, `<tr><td>%s</td><td class="kind">%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			e.at.Format("15:04:05.000"),
			html.EscapeString(string(e.d.Kind)),
			html.EscapeString(short(e.d.SessionID)),
			html.EscapeString(e.d.Detail),
			html.EscapeString(errText),
		)
	}
	fmt.Fprint(w, `</table>`)
}

// short truncates a session ID for display.
func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}
