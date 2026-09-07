package tether

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// These tests exercise the request's ownership check against tracked sessions;
// their command loops are already finished so teardown needs no background loop.
func newDestroyBindingSession(t *testing.T, h *Handler[counterState], state string) *StatefulSession[counterState] {
	t.Helper()
	s := newTestSession(counterState{Count: 42}, &mockTransport{})
	s.id, s.userAgent = newID(), "Browser/1"
	s.status.Store(int32(Active))
	if state == "frozen" {
		s.status.Store(int32(Frozen))
	}
	close(s.loopDone)
	t.Cleanup(s.stop)
	if state == "active" {
		h.active[s.id] = s
	} else {
		h.disconnected[s.id] = s
	}
	return s
}

func destroyBindingRequest(h *Handler[counterState], id, ua string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/?tether=destroy", strings.NewReader(id))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("User-Agent", ua)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDestroyBeaconSessionBinding(t *testing.T) {
	for _, state := range []string{"active", "disconnected", "frozen"} {
		for _, tt := range []struct {
			name     string
			ua       string
			disabled bool
			custom   bool
			allow    bool
		}{
			{name: "matching", ua: "Browser/1", allow: true},
			{name: "mismatched", ua: "Browser/2"},
			{name: "missing", ua: ""},
			{name: "disabled", ua: "Browser/2", disabled: true, custom: true, allow: true},
			{name: "custom accepts", ua: "Browser/2", custom: true, allow: true},
			{name: "custom rejects", ua: "Browser/2", custom: true},
		} {
			t.Run(state+"/"+tt.name, func(t *testing.T) {
				h := newReattachHandler()
				h.csrf = &http.CrossOriginProtection{}
				s := newDestroyBindingSession(t, h, state)
				h.app.Security.DisableSessionBinding = tt.disabled
				matched := false
				if tt.custom {
					h.app.Security.SessionMatch = func(original, incoming string) bool {
						matched = true
						// Application callbacks must run outside the pool mutex.
						_ = h.Health()
						if original != s.userAgent || incoming != tt.ua {
							t.Errorf("matcher arguments = (%q, %q)", original, incoming)
						}
						return tt.allow && !tt.disabled
					}
				}
				disconnected := 0
				h.cfg.OnDisconnect = func(*StatefulSession[counterState]) { disconnected++ }
				var got Diagnostic
				h.Diagnostics.Subscribe(t.Context(), func(d Diagnostic) { got = d })

				w := destroyBindingRequest(h, s.id, tt.ua)
				wantCode := http.StatusForbidden
				if tt.allow {
					wantCode = http.StatusNoContent
				}
				if w.Code != wantCode {
					t.Errorf("status = %d, want %d", w.Code, wantCode)
				}
				if matched != (tt.custom && !tt.disabled) {
					t.Errorf("custom matcher called = %v", matched)
				}
				tracked := h.active[s.id] == s || h.disconnected[s.id] == s
				if tt.allow {
					if tracked || s.ctx.Err() == nil || disconnected != 1 {
						t.Error("accepted destroy did not clean up the session exactly once")
					}
					if got.Kind != "" {
						t.Errorf("unexpected diagnostic: %+v", got)
					}
					if w := destroyBindingRequest(h, s.id, tt.ua); w.Code != http.StatusNoContent || disconnected != 1 {
						t.Error("repeated destroy was not idempotent")
					}
				} else {
					if !tracked || s.ctx.Err() != nil || disconnected != 0 {
						t.Error("rejected destroy changed the session lifetime or pool membership")
					}
					if got.Kind != SessionBindingFailed || got.SessionID != s.id || !strings.Contains(got.Detail, "destroy") {
						t.Errorf("binding diagnostic = %+v", got)
					}
				}
			})
		}
	}
}

func TestDestroyBeaconRechecksSessionIdentity(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "moves to disconnected"
		if replace {
			name = "another session takes ID"
		}
		t.Run(name, func(t *testing.T) {
			h := newReattachHandler()
			h.csrf = &http.CrossOriginProtection{}
			s := newDestroyBindingSession(t, h, "active")
			next := s
			if replace {
				next = newDestroyBindingSession(t, h, "disconnected")
				delete(h.disconnected, next.id)
				next.id, next.userAgent = s.id, "AnotherBrowser/1"
			}
			matched := false
			h.app.Security.SessionMatch = func(original, incoming string) bool {
				matched = true
				// Simulate a pool transition while application matching runs.
				h.mu.Lock()
				delete(h.active, s.id)
				h.disconnected[s.id] = next
				h.mu.Unlock()
				return original == incoming
			}
			w := destroyBindingRequest(h, s.id, s.userAgent)
			if w.Code != http.StatusNoContent || !matched {
				t.Fatalf("destroy status = %d, matcher called = %v", w.Code, matched)
			}
			if replace {
				if h.disconnected[s.id] != next || next.ctx.Err() != nil {
					t.Error("destroy removed a session that was never validated")
				}
			} else if h.disconnected[s.id] != nil || s.ctx.Err() == nil {
				t.Error("destroy lost the validated session when it changed pools")
			}
		})
	}
}

func TestReplacementSessionBinding(t *testing.T) {
	for _, tt := range []struct {
		name     string
		ua       string
		security Security
		allow    bool
	}{
		{name: "matching", ua: "Browser/1", allow: true},
		{name: "mismatched", ua: "Browser/2"},
		{name: "disabled", ua: "Browser/2", security: Security{DisableSessionBinding: true}, allow: true},
		{name: "custom", ua: "Browser/2", security: Security{
			SessionMatch: func(original, incoming string) bool {
				return strings.Split(original, "/")[0] == strings.Split(incoming, "/")[0]
			},
		}, allow: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newReattachHandler()
				h.app.Logger, h.app.Security = slog.Default(), tt.security
				s := newDestroyBindingSession(t, h, "disconnected")
				initialised := 0
				h.cfg.InitialState = func(*http.Request) counterState {
					initialised++
					return counterState{}
				}
				var got Diagnostic
				h.Diagnostics.Subscribe(t.Context(), func(d Diagnostic) { got = d })
				tok, ok := h.issueTicket(newID(), s.id, tt.ua, time.Now())
				if !ok {
					t.Fatal("connect ticket refused")
				}
				r := httptest.NewRequest(http.MethodGet, "/?ticket="+tok, nil)
				r.Header.Set("User-Agent", tt.ua)
				tr := newConnectedTransport()
				go h.serveSession(httptest.NewRecorder(), r, func(http.ResponseWriter, *http.Request) (Transport, error) { return tr, nil })
				synctest.Wait()

				if tt.allow {
					if h.disconnected[s.id] != nil || s.ctx.Err() == nil || initialised != 1 {
						t.Error("accepted handoff did not replace the old session")
					}
				} else {
					if h.disconnected[s.id] != s || s.ctx.Err() != nil || initialised != 0 {
						t.Error("rejected handoff destroyed the old session or initialised a new one")
					}
					if got.Kind != SessionBindingFailed || got.SessionID != s.id {
						t.Errorf("binding diagnostic = %+v", got)
					}
					tr.mu.Lock()
					closed := tr.closed
					tr.mu.Unlock()
					if !closed {
						t.Error("rejected handoff left the transport open")
					}
				}
				h.Shutdown(context.Background())
				synctest.Wait()
			})
		})
	}
}
