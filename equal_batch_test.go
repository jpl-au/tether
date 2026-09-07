package tether

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/wire"
)

// A batch can return to its initial state after an intermediate command has
// already changed the browser. Equal must not suppress the final correction.
func TestEqualBatchReconcilesIntermediateDOMUpdate(t *testing.T) {
	for _, memoise := range []bool{false, true} {
		name := "Differ"
		if memoise {
			name = "Memoiser"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				pendingUpdate bool
				sse           bool
				structural    bool
			}{
				{name: "PatchThenUpdate"},
				{name: "UpdateThenPatchThenUpdate", pendingUpdate: true},
				{name: "SSEEventThenUpdate", sse: true},
				{name: "UpdateThenSSEEventThenUpdate", pendingUpdate: true, sse: true},
				{name: "SSEMorphThenUpdate", sse: true, structural: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						ct := newConnectedTransport()
						sess := newTestSession(counterState{}, ct)
						sess.id = newID()
						sess.equal = func(a, b counterState) bool { return a == b }
						page := renderCounter
						if tc.structural {
							page = func(s counterState) node.Node {
								children := []node.Node{span.Textf("Count: %d", s.Count).Dynamic("count")}
								if s.Count != 0 {
									children = append(children, span.Static("Extra").Dynamic("extra"))
								}
								return div.New(children...)
							}
						}
						if memoise {
							sess.engine.Clear()
							sess.engine = jit.NewMemoiser()
						}
						sess.engine.RenderBytes(page(counterState{}))
						renders := 0
						sess.render = func(s counterState) node.Node {
							renders++
							return page(s)
						}

						// Queue everything before starting the loop, ensuring the
						// commands form one batch without scheduler-dependent sleeps.
						if tc.pendingUpdate {
							sess.Update(func(counterState) counterState { return counterState{Count: 2} })
						}
						intermediate := 1
						if tc.sse {
							h := &Handler[counterState]{
								active: map[string]*StatefulSession[counterState]{sess.id: sess},
								csrf:   &http.CrossOriginProtection{},
								cfg:    StatefulConfig[counterState]{Limits: Limits{MaxEventBytes: 4096}},
							}
							req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"type":"click","action":"increment","event_id":"batch-event"}`))
							req.Header.Set("Tether-Session", sess.id)
							response := httptest.NewRecorder()
							h.handlePostEvent(response, req)
							if response.Code != http.StatusNoContent {
								t.Fatalf("SSE event rejected: status=%d body=%s", response.Code, response.Body.String())
							}
							if tc.pendingUpdate {
								intermediate = 3
							}
						} else {
							sess.Patch("count", func(s counterState) (counterState, node.Node) {
								s.Count = 1
								return s, span.Text("Count: 1").Dynamic("count")
							})
						}
						sess.Update(func(counterState) counterState {
							sess.Toast("restored")
							return counterState{}
						})
						go sess.run()
						defer func() { sess.stop(); synctest.Wait() }()
						synctest.Wait()

						if got := sess.State().Count; got != 0 {
							t.Fatalf("server Count=%d, want 0", got)
						}
						ct.mu.Lock()
						messages := make([]testMessage, len(ct.sent))
						for i, raw := range ct.sent {
							messages[i] = decodeMessage(raw)
						}
						ct.mu.Unlock()
						if len(messages) != 2 {
							t.Fatalf("want intermediate update and correction, got %v", messages)
						}
						for i, count := range []int{intermediate, 0} {
							msg := messages[i]
							if tc.structural {
								want := string(page(counterState{Count: count}).RenderBytes())
								if len(msg.Morphs) != 1 || msg.Morphs[0].Key != "" || msg.Morphs[0].HTML != want {
									t.Errorf("update %d: want root morph for Count=%d, got %+v", i, count, msg)
								}
							} else {
								want := string(span.Textf("Count: %d", count).Dynamic("count").RenderBytes())
								if len(msg.Patches) != 1 || msg.Patches[0].Key != "count" || msg.Patches[0].HTML != want {
									t.Errorf("update %d: want count patch for Count=%d, got %+v", i, count, msg)
								}
							}
						}
						if tc.sse && messages[0].EventID != "batch-event" {
							t.Errorf("SSE event acknowledgement lost: %+v", messages[0])
						}
						if messages[1].Toast != "restored" || messages[1].EventID != "" {
							t.Errorf("incorrect correction effects or event ID: %+v", messages[1])
						}

						// An ordinary batch must still skip rendering, even after
						// the previous batch sent DOM updates. Hold the loop while
						// queueing both mutations so they cannot run separately.
						before := renders
						entered, release := make(chan struct{}), make(chan struct{})
						sess.enqueue(func() { close(entered); <-release })
						<-entered
						sess.Update(func(counterState) counterState { return counterState{Count: 9} })
						sess.Update(func(counterState) counterState {
							sess.Toast("unchanged")
							return counterState{}
						})
						close(release)
						synctest.Wait()
						if renders != before {
							t.Errorf("ordinary equal batch rendered %d extra times", renders-before)
						}
						ct.mu.Lock()
						defer ct.mu.Unlock()
						if len(ct.sent) != 3 {
							t.Fatalf("want one effects-only update from equal batch, got %d messages", len(ct.sent))
						}
						last := decodeMessage(ct.sent[2])
						if last.Toast != "unchanged" || len(last.Patches) != 0 || len(last.Morphs) != 0 {
							t.Errorf("incorrect equal-batch effects: %+v", last)
						}
					})
				})
			}
		})
	}
}

func TestEqualBatchSkipsRenderWithoutDOMUpdate(t *testing.T) {
	for _, action := range []string{"EffectsOnly", "UnchangedPatch"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sess := newTestSession(counterState{}, newConnectedTransport())
				sess.equal = func(a, b counterState) bool { return a == b }
				renders := 0
				sess.render = func(s counterState) node.Node { renders++; return renderCounter(s) }
				sess.Update(func(s counterState) counterState { return s })
				if action == "EffectsOnly" {
					sess.enqueue(func() { sess.send(wire.Update{EventID: "ack", Toast: "message"}) })
				} else {
					sess.Patch("count", func(s counterState) (counterState, node.Node) {
						return s, span.Text("Count: 0").Dynamic("count")
					})
				}
				sess.Update(func(s counterState) counterState { return s })
				go sess.run()
				defer func() { sess.stop(); synctest.Wait() }()
				synctest.Wait()
				if renders != 0 {
					t.Errorf("equal batch without DOM updates rendered %d times", renders)
				}
			})
		})
	}
}
