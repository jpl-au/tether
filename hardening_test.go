package tether

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/wire"
)

// waitFor polls until cond returns true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestInternalStopRemovesSessionFromActivePool covers the unified
// destruction path: a session destroyed from inside (stop - the same
// exit taken by idle timeout, MaxLifetime, and unrecovered panics)
// must be removed from the active pool, leave its groups, and fire
// OnDisconnect. Previously it leaked in h.active forever.
func TestInternalStopRemovesSessionFromActivePool(t *testing.T) {
	group := NewGroup[counterState]()
	sessCh := make(chan *StatefulSession[counterState], 1)
	disconnected := make(chan struct{}, 1)

	h := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      func(w http.ResponseWriter, r *http.Request) (Transport, error) { return newConnectedTransport(), nil },
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Groups:       []*Group[counterState]{group},
		OnConnect:    func(s *StatefulSession[counterState]) { sessCh <- s },
		OnDisconnect: func(*StatefulSession[counterState]) { disconnected <- struct{}{} },
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Upgrade", "websocket")
	go h.ServeHTTP(httptest.NewRecorder(), req)

	var sess *StatefulSession[counterState]
	select {
	case sess = <-sessCh:
	case <-time.After(2 * time.Second):
		t.Fatal("session never connected")
	}
	waitFor(t, "group membership", func() bool { return group.Len() == 1 })

	// Destroy from inside the session - not via the transport.
	sess.stop()
	<-sess.destroyed

	waitFor(t, "active pool removal", func() bool {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return len(h.active) == 0 && len(h.disconnected) == 0
	})
	waitFor(t, "group removal", func() bool { return group.Len() == 0 })
	waitFor(t, "group count sync", func() bool { return group.Count().Load() == 0 })

	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("OnDisconnect did not fire for internally destroyed session")
	}

	// With the pools empty, Drain must return promptly instead of
	// hanging on its context for a session that no longer exists.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Drain(ctx); err != nil {
		t.Fatalf("Drain after internal destroy: %v", err)
	}
	h.Shutdown(context.Background())
}

// TestInitialGETRedirectsOnNavigate covers the pre-warm effects fix:
// an auth guard calling Navigate in OnNavigate during the initial GET
// must produce a real HTTP redirect, not the guarded page's HTML.
func TestInitialGETRedirectsOnNavigate(t *testing.T) {
	h := Stateful(App{}, StatefulConfig[counterState]{
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		OnNavigate: func(sess Session, s counterState, p Params) counterState {
			if p.Path == "/private" {
				sess.Navigate("/login")
			}
			return s
		},
	})

	req := httptest.NewRequest("GET", "/private", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	if strings.Contains(w.Body.String(), "data-tether-session") {
		t.Error("redirect response should not carry a session")
	}
}

// TestInitialGETTitleEffectReachesClient covers the other half of the
// pre-warm effects fix: SetTitle during the initial GET must arrive in
// the first update after the transport connects.
func TestInitialGETTitleEffectReachesClient(t *testing.T) {
	ctCh := make(chan *connectedTransport, 1)
	h := Stateful(App{}, StatefulConfig[counterState]{
		Mode: mode.WebSocket,
		Upgrade: func(w http.ResponseWriter, r *http.Request) (Transport, error) {
			ct := newConnectedTransport()
			ctCh <- ct
			return ct, nil
		},
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		OnNavigate: func(sess Session, s counterState, p Params) counterState {
			sess.SetTitle("Pre-warmed Title")
			return s
		},
	})

	// Initial GET creates the pending session and captures the title.
	getReq := httptest.NewRequest("GET", "/", nil)
	getReq.Header.Set("User-Agent", "TestBrowser/1.0")
	h.ServeHTTP(httptest.NewRecorder(), getReq)

	h.mu.RLock()
	var id string
	for k := range h.pending {
		id = k
	}
	h.mu.RUnlock()
	if id == "" {
		t.Fatal("no pending session created by initial GET")
	}

	// Connect the transport, claiming the pending session.
	req := httptest.NewRequest("GET", connectPath(h, id, "TestBrowser/1.0"), nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("User-Agent", "TestBrowser/1.0")
	go h.ServeHTTP(httptest.NewRecorder(), req)

	var ct *connectedTransport
	select {
	case ct = <-ctCh:
	case <-time.After(2 * time.Second):
		t.Fatal("transport never upgraded")
	}

	waitFor(t, "title update", func() bool {
		ct.mu.Lock()
		defer ct.mu.Unlock()
		for _, data := range ct.sent {
			if decodeMessage(data).Title == "Pre-warmed Title" {
				return true
			}
		}
		return false
	})
	h.Shutdown(context.Background())
}

// TestStaleClientReceivesFullMorph covers the stale-client recovery
// fix: a client reconnecting with a session ID the server no longer
// knows must receive a full morph (and its new session ID) without
// having to trigger any event.
func TestStaleClientReceivesFullMorph(t *testing.T) {
	ctCh := make(chan *connectedTransport, 1)
	h := Stateful(App{}, StatefulConfig[counterState]{
		Mode: mode.WebSocket,
		Upgrade: func(w http.ResponseWriter, r *http.Request) (Transport, error) {
			ct := newConnectedTransport()
			ctCh <- ct
			return ct, nil
		},
		InitialState: func(r *http.Request) counterState { return counterState{Count: 3} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})

	// The ID comes from a previous server instance - nothing in any
	// pool or store.
	req := httptest.NewRequest("GET", connectPath(h, "GONESESSIONFROMOLDNODE123", "TestBrowser/1.0"), nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("User-Agent", "TestBrowser/1.0")
	go h.ServeHTTP(httptest.NewRecorder(), req)

	var ct *connectedTransport
	select {
	case ct = <-ctCh:
	case <-time.After(2 * time.Second):
		t.Fatal("transport never upgraded")
	}

	waitFor(t, "full morph for stale client", func() bool {
		ct.mu.Lock()
		defer ct.mu.Unlock()
		for _, data := range ct.sent {
			msg := decodeMessage(data)
			for _, m := range msg.Morphs {
				if m.Key == "" && strings.Contains(m.HTML, "Count: 3") {
					return true
				}
			}
		}
		return false
	})
	h.Shutdown(context.Background())
}

// TestConnectTicketLifecycle covers the one-time connect ticket:
// single use, expiry, and User-Agent binding.
func TestConnectTicketLifecycle(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	tok, ok := h.issueTicket("SESSIONIDSESSIONIDSESSIONID", "", "UA/1.0", now)
	if !ok {
		t.Fatal("issueTicket refused")
	}

	if _, ok := h.redeemTicket(tok, "UA/2.0"); ok {
		t.Error("ticket redeemed with mismatched User-Agent")
	}

	tok, _ = h.issueTicket("SESSIONIDSESSIONIDSESSIONID", "OLDSESSIONOLDSESSION", "UA/1.0", now)
	got, ok := h.redeemTicket(tok, "UA/1.0")
	if !ok {
		t.Fatal("valid ticket not redeemed")
	}
	if got.session != "SESSIONIDSESSIONIDSESSIONID" || got.replaces != "OLDSESSIONOLDSESSION" {
		t.Errorf("ticket carried %q/%q", got.session, got.replaces)
	}
	if _, ok := h.redeemTicket(tok, "UA/1.0"); ok {
		t.Error("ticket redeemed twice")
	}

	tok, _ = h.issueTicket("SESSIONIDSESSIONIDSESSIONID", "", "UA/1.0", now.Add(-2*connectTicketTTL))
	if _, ok := h.redeemTicket(tok, "UA/1.0"); ok {
		t.Error("expired ticket redeemed")
	}
}

// TestDestroyBeaconRequiresPOST covers the hardened destroy beacon: a
// cross-site GET (e.g. an <img> tag) must not be able to destroy a
// session, and the ID travels in the body, not the URL.
func TestDestroyBeaconRequiresPOST(t *testing.T) {
	h := newTestHandler()
	sess := newTestSession(counterState{}, &mockTransport{})
	sess.handler = h
	h.mu.Lock()
	h.disconnected[sess.id] = sess
	h.mu.Unlock()

	// GET must be rejected outright.
	req := httptest.NewRequest("GET", "/?tether=destroy", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET destroy: status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}

	// Cross-site POST must be blocked by the CSRF check.
	req = httptest.NewRequest("POST", "/?tether=destroy", strings.NewReader(sess.id))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site destroy: status = %d, want %d", w.Code, http.StatusForbidden)
	}
	h.mu.RLock()
	_, still := h.disconnected[sess.id]
	h.mu.RUnlock()
	if !still {
		t.Fatal("session destroyed by a rejected request")
	}

	// A same-origin POST with the ID in the body destroys the session.
	req = httptest.NewRequest("POST", "/?tether=destroy", strings.NewReader(sess.id))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("destroy: status = %d, want %d", w.Code, http.StatusNoContent)
	}
	h.mu.RLock()
	_, still = h.disconnected[sess.id]
	h.mu.RUnlock()
	if still {
		t.Error("session not destroyed by valid beacon")
	}
}

// TestRestoreRespectsDrainAndMaxSessions covers the restore gate: a
// draining or full node must not build sessions from the store.
func TestRestoreRespectsDrainAndMaxSessions(t *testing.T) {
	store := newSessionFileStore(t)
	h := newRestoreHandler(store)
	saveToStore(t, store, "RESTOREGATETEST01", counterState{Count: 1})

	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "TestAgent/1.0")

	h.draining.Store(true)
	if _, ok := h.restoreSession("RESTOREGATETEST01", req, &mockTransport{}); ok {
		t.Error("restoreSession accepted while draining")
	}
	h.draining.Store(false)

	h.app.MaxSessions = 1
	h.mu.Lock()
	h.active["OCCUPANT"] = &StatefulSession[counterState]{id: "OCCUPANT"}
	h.mu.Unlock()
	if _, ok := h.restoreSession("RESTOREGATETEST01", req, &mockTransport{}); ok {
		t.Error("restoreSession accepted past MaxSessions")
	}
}

// binaryCaptureTransport records SendBinary payloads separately from
// text sends so tests can assert which framing the session chose.
type binaryCaptureTransport struct {
	mockTransport
	binary [][]byte
}

func (b *binaryCaptureTransport) SendBinary(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	b.binary = append(b.binary, cp)
	return nil
}

// TestCBORUsesBinaryFramesWhenSupported covers the wire-format fix:
// CBOR sessions send native binary frames on transports that support
// them (WebSocket) and fall back to base64 text on those that don't
// (SSE), instead of paying the +33% base64 inflation everywhere.
func TestCBORUsesBinaryFramesWhenSupported(t *testing.T) {
	bt := &binaryCaptureTransport{}
	sess := newTestSession(counterState{}, bt)
	sess.encoder = wire.CBOREncoder{}
	sess.wireFormat = wire.CBOR

	sess.send(wire.Update{Toast: "hello"})

	bt.mu.Lock()
	binFrames, textFrames := len(bt.binary), len(bt.sent)
	bt.mu.Unlock()
	if binFrames != 1 {
		t.Fatalf("binary frames = %d, want 1", binFrames)
	}
	if textFrames != 0 {
		t.Errorf("text frames = %d, want 0 for a binary-capable transport", textFrames)
	}

	// A transport without SendBinary gets base64-encoded text.
	mt := &mockTransport{}
	sess2 := newTestSession(counterState{}, mt)
	sess2.encoder = wire.CBOREncoder{}
	sess2.wireFormat = wire.CBOR

	sess2.send(wire.Update{Toast: "hello"})

	mt.mu.Lock()
	defer mt.mu.Unlock()
	if len(mt.sent) != 1 {
		t.Fatalf("text frames = %d, want 1", len(mt.sent))
	}
	if _, err := base64.StdEncoding.DecodeString(string(mt.sent[0])); err != nil {
		t.Errorf("text fallback is not valid base64: %v", err)
	}
}

// TestDebugDashboard covers the dev-only observability page: it must
// show the pool counts and recent diagnostics in dev mode and be
// absent in production, where Health() is the monitoring surface.
func TestDebugDashboard(t *testing.T) {
	t.Run("dev mode serves the dashboard", func(t *testing.T) {
		t.Cleanup(dev.Reset)
		h := Stateful(App{DevMode: true}, StatefulConfig[counterState]{
			Mode:         mode.WebSocket,
			Upgrade:      stubUpgrade,
			InitialState: func(r *http.Request) counterState { return counterState{} },
			Render:       renderCounter,
			Handle:       handleCounter,
		})

		h.Diagnostics.Publish(Diagnostic{Kind: TransportError, SessionID: "DASHTESTSESSION", Detail: "/app"})

		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/_tether/debug", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "Sessions") || !strings.Contains(body, "Diagnostics") {
			t.Errorf("dashboard missing sections: %s", body)
		}
		if !strings.Contains(body, string(TransportError)) {
			t.Error("published diagnostic not shown on dashboard")
		}
	})

	t.Run("production does not expose it", func(t *testing.T) {
		h := newTestHandler()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/_tether/debug", nil))
		if strings.Contains(w.Body.String(), "tether debug") {
			t.Error("debug dashboard reachable outside dev mode")
		}
	})
}
