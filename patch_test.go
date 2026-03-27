package tether

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

func TestPatchUpdatesTargetedKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()

		// Patch the "count" key with new content.
		sess.Patch("count", func(s counterState) (counterState, node.Node) {
			s.Count = 42
			return s, span.Text("42").Dynamic("count")
		})
		synctest.Wait()

		// State should be updated.
		if sess.State().Count != 42 {
			t.Errorf("expected Count=42, got %d", sess.State().Count)
		}

		// Transport should have received a patch.
		ct.mu.Lock()
		sent := ct.sent
		ct.mu.Unlock()

		if len(sent) == 0 {
			t.Fatal("expected at least one message sent")
		}

		// Find a message with a patch for "count" in the JSON.
		found := false
		for _, msg := range sent {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(msg, &raw); err != nil {
				continue
			}
			if patches, ok := raw["patches"]; ok {
				if strings.Contains(string(patches), `"count"`) {
					found = true
				}
			}
		}
		if !found {
			t.Error("expected a patch for key 'count' in sent messages")
		}

		sess.stop()
		synctest.Wait()
	})
}

func TestPatchNoChangeDoesNotSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ct := newConnectedTransport()
		sess := newTestSession(counterState{Count: 0}, ct)

		go sess.readTransport(sess.events)
		go sess.run()

		// Wait for initial render to settle.
		synctest.Wait()

		ct.mu.Lock()
		beforeCount := len(ct.sent)
		ct.mu.Unlock()

		// Patch with identical content for "count" - no message should be sent.
		sess.Patch("count", func(s counterState) (counterState, node.Node) {
			return s, span.Textf("Count: %d", s.Count).Dynamic("count")
		})
		synctest.Wait()

		ct.mu.Lock()
		afterCount := len(ct.sent)
		ct.mu.Unlock()

		// The patch produces the same content as the current snapshot,
		// so DiffKey returns nil and no message is sent.
		if afterCount != beforeCount {
			t.Errorf("expected no new messages for unchanged patch, got %d new", afterCount-beforeCount)
		}

		sess.stop()
		synctest.Wait()
	})
}
