package tether

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/wire"
)

// newReattachHandler builds a Handler that keeps disconnected sessions
// alive for the reconnect window. Shared setup for the catch-up tests.
func newReattachHandler() *Handler[counterState] {
	h := &Handler[counterState]{
		app: App{},
		cfg: StatefulConfig[counterState]{
			Render:   renderCounter,
			Handle:   handleCounter,
			Limits:   Limits{CmdBufferSize: defaultCmdBufferSize},
			Timeouts: Timeouts{Reconnect: 30 * time.Second},
		},
		pending:      make(map[string]*pendingSession[counterState]),
		active:       make(map[string]*StatefulSession[counterState]),
		disconnected: make(map[string]*StatefulSession[counterState]),
		done:         make(chan struct{}),
		encoder:      wire.JSONEncoder{},
	}
	h.Diagnostics = NewBus[Diagnostic]()
	return h
}

// disconnectedSession runs a session to transport EOF so it is sitting
// in the reconnect window, then returns it ready to be reattached.
func disconnectedSession(t *testing.T, h *Handler[counterState]) *StatefulSession[counterState] {
	t.Helper()
	sess := newTestSession(counterState{Count: 0}, &mockTransport{})
	sess.reconnectTimeout = h.cfg.Timeouts.Reconnect
	sess.diagnostics = h.Diagnostics

	h.mu.Lock()
	h.active[sess.id] = sess
	h.mu.Unlock()
	sess.handler = h

	go sess.readTransport(sess.events)
	go sess.run()
	synctest.Wait()

	if sess.transport != nil {
		t.Fatal("expected the transport to be released after EOF")
	}
	return sess
}

// TestReattachSendsChangesMadeWhileDisconnected is the regression test
// for the catch-up contract: a state change applied while the client is
// away must reach that client when it comes back. Renders are deferred
// during the reconnect window so the engine's baseline keeps describing
// the DOM the browser is actually showing - if the baseline advanced
// instead, this diff would come back empty and the client would display
// "Count: 0" for the rest of the session.
func TestReattachSendsChangesMadeWhileDisconnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		sess := disconnectedSession(t, h)

		sess.Update(func(s counterState) counterState {
			s.Count = 42
			return s
		})
		synctest.Wait()

		if got := sess.State().Count; got != 42 {
			t.Fatalf("state should update while disconnected: got %d, want 42", got)
		}

		ct := newConnectedTransport()
		h.reattach(sess, ct)
		synctest.Wait()

		ct.mu.Lock()
		sent := append([][]byte(nil), ct.sent...)
		ct.mu.Unlock()

		var found bool
		for _, raw := range sent {
			for _, p := range decodeMessage(raw).Patches {
				if p.Key == "count" && strings.Contains(p.HTML, "Count: 42") {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("reattach did not send the change made while disconnected; sent %d message(s): %s",
				len(sent), sent)
		}

		sess.stop()
		ct.Close()
		synctest.Wait()
	})
}

// TestReattachAfterPatchWhileDisconnected covers the same contract for
// Patch, which advances a single key's snapshot rather than the whole
// tree and so strands the client just as easily.
func TestReattachAfterPatchWhileDisconnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		sess := disconnectedSession(t, h)

		sess.Patch("count", func(s counterState) (counterState, node.Node) {
			s.Count = 7
			return s, renderCounter(s).Nodes()[0]
		})
		synctest.Wait()

		ct := newConnectedTransport()
		h.reattach(sess, ct)
		synctest.Wait()

		ct.mu.Lock()
		sent := append([][]byte(nil), ct.sent...)
		ct.mu.Unlock()

		var found bool
		for _, raw := range sent {
			for _, p := range decodeMessage(raw).Patches {
				if p.Key == "count" && strings.Contains(p.HTML, "Count: 7") {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("reattach did not send the patch made while disconnected; sent %d message(s): %s",
				len(sent), sent)
		}

		sess.stop()
		ct.Close()
		synctest.Wait()
	})
}

// TestReattachDeliversHeldEffects covers the effects half of the
// catch-up contract: a notification raised while the client is away is
// held and arrives with the patches on reconnect, rather than being
// dropped for want of a transport.
func TestReattachDeliversHeldEffects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		sess := disconnectedSession(t, h)

		sess.Toast("export ready")
		sess.Signal("exports", 3)
		sess.Flash("#notice", "saved")
		synctest.Wait()

		ct := newConnectedTransport()
		h.reattach(sess, ct)
		synctest.Wait()

		ct.mu.Lock()
		sent := append([][]byte(nil), ct.sent...)
		ct.mu.Unlock()

		var toast, flash string
		var signal any
		for _, raw := range sent {
			m := decodeMessage(raw)
			if m.Toast != "" {
				toast = m.Toast
			}
			if v, ok := m.Signals["exports"]; ok {
				signal = v
			}
			if v, ok := m.Flash["#notice"]; ok {
				flash = v
			}
		}
		if toast != "export ready" {
			t.Errorf("held toast not delivered on reattach: got %q", toast)
		}
		if signal != float64(3) {
			t.Errorf("held signal not delivered on reattach: got %v", signal)
		}
		if flash != "saved" {
			t.Errorf("held flash not delivered on reattach: got %q", flash)
		}

		sess.stop()
		ct.Close()
		synctest.Wait()
	})
}

// TestDisconnectedTransientEffectsDropped pins the other half of the
// rule: effects that tell the browser to act name a moment that has
// passed, so they are dropped rather than fired late, and the drop is
// reported by name.
func TestDisconnectedTransientEffectsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()

		var details []string
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.Diagnostics.Subscribe(ctx, func(d Diagnostic) {
			if d.Kind == CommandDiscarded {
				details = append(details, d.Detail)
			}
		})

		sess := disconnectedSession(t, h)
		sess.Download("/export/report.csv")
		sess.ScrollTo("#top")
		synctest.Wait()

		ct := newConnectedTransport()
		h.reattach(sess, ct)
		synctest.Wait()

		ct.mu.Lock()
		sent := append([][]byte(nil), ct.sent...)
		ct.mu.Unlock()
		for _, raw := range sent {
			if s := string(raw); strings.Contains(s, "report.csv") || strings.Contains(s, "#top") {
				t.Errorf("transient effect replayed on reattach: %s", s)
			}
		}

		var named bool
		for _, d := range details {
			if strings.Contains(d, "Download") || strings.Contains(d, "ScrollTo") {
				named = true
			}
		}
		if !named {
			t.Errorf("expected a CommandDiscarded naming the dropped effects, got %v", details)
		}

		sess.stop()
		ct.Close()
		synctest.Wait()
	})
}

// TestHeldNavigateResolvesServerSide verifies that a Navigate raised
// while the client was away runs through Handle before the catch-up
// diff, so the state matches the URL the client is about to show, and
// that the address bar is synced with Replace rather than pushed (which
// would make the client echo a navigate event straight back).
func TestHeldNavigateResolvesServerSide(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		var navigated []string
		h.cfg.Handle = func(_ Session, s counterState, ev Event) counterState {
			if ev.Type == event.Navigate {
				navigated = append(navigated, ev.Data["path"])
				s.Count = 99
			}
			return s
		}

		sess := disconnectedSession(t, h)
		sess.handle = h.cfg.Handle
		sess.Navigate("/checkout")
		synctest.Wait()

		ct := newConnectedTransport()
		h.reattach(sess, ct)
		synctest.Wait()

		if len(navigated) != 1 || navigated[0] != "/checkout" {
			t.Errorf("held navigate did not resolve server-side: %v", navigated)
		}
		if got := sess.State().Count; got != 99 {
			t.Errorf("state did not catch up with the held navigate: got %d", got)
		}

		ct.mu.Lock()
		sent := append([][]byte(nil), ct.sent...)
		ct.mu.Unlock()

		var sawURL bool
		for _, raw := range sent {
			m := decodeMessage(raw)
			if m.URL == "" {
				continue
			}
			sawURL = true
			if m.URL != "/checkout" {
				t.Errorf("catch-up URL = %q, want /checkout", m.URL)
			}
			if !m.Replace {
				t.Error("held navigate should sync the address bar with replace, not push a history entry the client echoes back")
			}
		}
		if !sawURL {
			t.Error("catch-up did not carry the held navigation URL")
		}

		sess.stop()
		ct.Close()
		synctest.Wait()
	})
}

// TestHeldNavigateSurvivesShutdown guards the session-store path: the
// URL a session was sent to while away must reach the envelope, or a
// rolling deploy restores the client on the page it left.
func TestHeldNavigateSurvivesShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		sess := disconnectedSession(t, h)

		sess.Navigate("/checkout")
		sess.SetTitle("Checkout")
		synctest.Wait()

		if sess.lastURL != "/checkout" {
			t.Errorf("lastURL = %q, want /checkout - Shutdown persists this into the session envelope", sess.lastURL)
		}
		if sess.lastTitle != "Checkout" {
			t.Errorf("lastTitle = %q, want Checkout", sess.lastTitle)
		}

		sess.stop()
		synctest.Wait()
	})
}

// TestUndeliveredEffectsReportedOnDestroy verifies that a session which
// dies still holding effects says so, rather than swallowing a
// developer's Toast in silence.
func TestUndeliveredEffectsReportedOnDestroy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()

		var reported bool
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.Diagnostics.Subscribe(ctx, func(d Diagnostic) {
			if d.Kind == CommandDiscarded && strings.Contains(d.Detail, "undelivered") {
				reported = true
			}
		})

		sess := disconnectedSession(t, h)
		sess.Toast("nobody came back")
		synctest.Wait()

		sess.stop()
		synctest.Wait()

		if !reported {
			t.Error("expected a CommandDiscarded when the session ended holding effects")
		}
	})
}
