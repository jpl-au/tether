package tether

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpl-au/fluent/node"
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

// TestDisconnectedEffectsReportDiscard verifies that effects raised
// while the client is away are reported rather than lost in silence -
// unlike state, they have no catch-up path.
func TestDisconnectedEffectsReportDiscard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()

		var discarded int
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.Diagnostics.Subscribe(ctx, func(d Diagnostic) {
			if d.Kind == CommandDiscarded && d.Detail == "disconnected" {
				discarded++
			}
		})

		sess := disconnectedSession(t, h)
		sess.Toast("nobody is listening")
		synctest.Wait()

		if discarded == 0 {
			t.Error("expected a CommandDiscarded diagnostic for the undeliverable toast")
		}

		sess.stop()
		synctest.Wait()
	})
}
