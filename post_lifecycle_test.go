package tether

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/jpl-au/tether/push"
)

func TestPostEventSessionLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       Status
		disconnected bool
		want         int
	}{
		{"active", Active, false, 204}, {"disconnected", Active, true, 204},
		{"frozen", Frozen, true, 503}, {"destroyed", Destroyed, false, 410},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newReattachHandler()
				h.csrf = &http.CrossOriginProtection{}
				h.cfg.Limits.MaxEventBytes = 4096
				s := newTestSession(counterState{}, newConnectedTransport())
				s.id = newID()
				s.status.Store(int32(tc.status))
				if tc.disconnected {
					s.transport = nil
					h.disconnected[s.id] = s
				} else {
					h.active[s.id] = s
				}
				if tc.status == Destroyed {
					s.stop()
				}
				r := httptest.NewRequest("POST", "/", strings.NewReader(`{"type":"click","action":"increment"}`))
				r.Header.Set("Tether-Session", s.id)
				w := httptest.NewRecorder()
				h.handlePostEvent(w, r)
				if w.Code != tc.want {
					t.Errorf("status=%d, want %d", w.Code, tc.want)
				}
				if tc.want == 204 {
					go s.run()
					synctest.Wait()
					if s.State().Count != 1 {
						t.Errorf("accepted event was not applied: %+v", s.State())
					}
					s.stop()
					synctest.Wait()
				} else if len(s.cmds) != 0 {
					t.Error("unavailable session accepted an event into an abandoned queue")
				}
				s.stop()
			})
		})
	}
}

func TestPushSubscriptionInstalledBeforeCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newReattachHandler()
		h.csrf = &http.CrossOriginProtection{}
		h.cfg.Limits.MaxPushSubscriptionBytes = 4096
		s := newTestSession(counterState{}, newConnectedTransport())
		s.id = newID()
		h.disconnected[s.id] = s
		called := false
		h.cfg.Push = &PushConfig[counterState]{OnSubscribe: func(_ context.Context, session *StatefulSession[counterState], sub push.Subscription) {
			called = true
			got := session.pushSub.Load()
			if got == nil || got.Endpoint != sub.Endpoint {
				t.Error("OnSubscribe ran before subscription installation")
			}
		}}
		key := make([]byte, 65)
		key[0] = 4
		body, err := json.Marshal(push.Subscription{Endpoint: "https://push.example.test/send", Keys: push.SubscriptionKeys{P256dh: base64.RawURLEncoding.EncodeToString(key), Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
		r.Header.Set("Tether-Session", s.id)
		w := httptest.NewRecorder()
		h.handlePushSubscribe(w, r)
		synctest.Wait()
		if w.Code != 204 || !called {
			t.Errorf("subscription not accepted: status=%d callback=%t", w.Code, called)
		}
		if len(s.cmds) != 0 {
			t.Error("atomic subscription installation unnecessarily queued")
		}
		s.stop()
		synctest.Wait()
	})
}
