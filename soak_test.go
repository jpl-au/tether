package tether

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpl-au/tether/mode"
)

// The lifecycle stress harness drives many sessions concurrently
// through the full state machine - connect, events, disconnect,
// reconnect (thaw when freeze is enabled), stale reconnect, beacon
// destroy, and reconnect-timer expiry - under -race, then asserts the
// accounting invariants the audit found violated: every session must
// leave the pools, groups must match membership, Drain must return,
// and no goroutines may leak.

var sessionAttrRe = regexp.MustCompile(`data-tether-session="([^"]+)"`)

// soakClient is one simulated browser tab. It owns the transports the
// upgrade callback hands back for its requests, correlated by a
// per-worker request header.
type soakClient struct {
	t  *testing.T
	h  *Handler[counterState]
	ua string
	ch chan *connectedTransport
}

// getPage performs the initial GET and returns the pre-warmed session
// ID embedded in the HTML.
func (c *soakClient) getPage() string {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", c.ua)
	c.h.ServeHTTP(w, req)
	m := sessionAttrRe.FindStringSubmatch(w.Body.String())
	if m == nil {
		c.t.Error("initial page carried no session ID")
		return ""
	}
	return m[1]
}

// connect opens a transport for the given session ID via the ticket
// flow and returns it. The ServeHTTP call blocks for the attachment's
// lifetime, so it runs on its own goroutine like a real request.
func (c *soakClient) connect(id string) *connectedTransport {
	req := httptest.NewRequest("GET", connectPath(c.h, id, c.ua), nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("X-Soak-Client", c.ua)
	go c.h.ServeHTTP(httptest.NewRecorder(), req)

	select {
	case ct := <-c.ch:
		return ct
	case <-time.After(5 * time.Second):
		c.t.Error("transport never upgraded")
		return nil
	}
}

// sendEvents pushes n increment events through the transport.
func (c *soakClient) sendEvents(ct *connectedTransport, n int) {
	for range n {
		select {
		case ct.ch <- Event{Type: "event", Action: "increment"}:
		case <-time.After(5 * time.Second):
			c.t.Error("event send stalled")
			return
		}
	}
}

// destroyBeacon simulates the page-unload beacon.
func (c *soakClient) destroyBeacon(id string) {
	req := httptest.NewRequest("POST", "/?tether=destroy", strings.NewReader(id))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	c.h.ServeHTTP(httptest.NewRecorder(), req)
}

// awaitDisconnected polls until the server has processed this
// session's transport close (it has left the active pool), so a
// reconnect finds it in the disconnected pool rather than racing the
// disconnect and being treated as a stale client.
func (c *soakClient) awaitDisconnected(id string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.h.mu.RLock()
		_, active := c.h.active[id]
		c.h.mu.RUnlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	c.t.Error("session never left the active pool after transport close")
}

func TestLifecycleStress(t *testing.T) {
	if testing.Short() {
		t.Skip("lifecycle stress harness - skipped with -short")
	}

	baseline := runtime.NumGoroutine()

	const workers = 8
	const iterations = 12

	group := NewGroup[counterState]()
	store := newSessionFileStore(t)

	// Route each upgraded transport back to the client that asked for
	// it, keyed by a request header.
	var mu sync.Mutex
	clients := make(map[string]*soakClient, workers)

	h := Stateful(App{}, StatefulConfig[counterState]{
		Mode: mode.WebSocket,
		Upgrade: func(w http.ResponseWriter, r *http.Request) (Transport, error) {
			ct := newConnectedTransport()
			mu.Lock()
			c := clients[r.Header.Get("X-Soak-Client")]
			mu.Unlock()
			if c != nil {
				c.ch <- ct
			}
			return ct, nil
		},
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
		Groups:       []*Group[counterState]{group},
		SessionStore: store,
		Freeze:       FreezeWithConnect,
		Timeouts: Timeouts{
			Reconnect: 150 * time.Millisecond,
		},
	})

	var wg sync.WaitGroup
	for w := range workers {
		c := &soakClient{
			t:  t,
			h:  h,
			ua: "Soak/" + string(rune('A'+w)),
			ch: make(chan *connectedTransport, 1),
		}
		mu.Lock()
		clients[c.ua] = c
		mu.Unlock()

		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for range iterations {
				id := c.getPage()
				if id == "" {
					return
				}
				ct := c.connect(id)
				if ct == nil {
					return
				}
				c.sendEvents(ct, 3)

				switch rng.Intn(4) {
				case 0:
					// Clean unload: beacon destroy while connected.
					c.destroyBeacon(id)
				case 1:
					// Disconnect, reconnect (thaw), more events,
					// then unload.
					ct.Close()
					c.awaitDisconnected(id)
					ct2 := c.connect(id)
					if ct2 == nil {
						return
					}
					c.sendEvents(ct2, 3)
					c.destroyBeacon(id)
				case 2:
					// Disconnect and abandon: the reconnect timer
					// destroys the session.
					ct.Close()
				case 3:
					// Stale reconnect: present an ID from a "previous
					// server instance". The server assigns a fresh
					// session; abandon it to the reconnect timer.
					ct.Close()
					c.awaitDisconnected(id)
					ct2 := c.connect("STALE" + id)
					if ct2 == nil {
						return
					}
					c.sendEvents(ct2, 2)
					ct2.Close()
				}
			}
		}(int64(w))
	}
	wg.Wait()

	// Abandoned sessions ride the 150ms reconnect timer; poll until
	// every pool is empty.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		hs := h.Health()
		if hs.Pending == 0 && hs.Active == 0 && hs.Disconnected == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hs := h.Health(); hs.Pending != 0 || hs.Active != 0 || hs.Disconnected != 0 {
		t.Fatalf("session accounting leak: pending=%d active=%d disconnected=%d",
			hs.Pending, hs.Active, hs.Disconnected)
	}

	// With the pools empty, Drain must return immediately.
	drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Drain(drainCtx); err != nil {
		t.Fatalf("Drain with empty pools: %v", err)
	}
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Group membership must match the (now empty) session set, and
	// its reactive count must agree.
	if n := group.Len(); n != 0 {
		t.Errorf("group.Len() = %d after all sessions destroyed, want 0", n)
	}
	if n := group.Count().Load(); n != 0 {
		t.Errorf("group.Count() = %d after all sessions destroyed, want 0", n)
	}

	// Every goroutine the harness spawned - session loops, transport
	// readers, HTTP handlers, timers, the pending reaper - must be
	// gone. Poll briefly: exits are asynchronous after Shutdown.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	t.Errorf("goroutine leak: baseline %d, now %d\n%s",
		baseline, runtime.NumGoroutine(), buf[:n])
}
