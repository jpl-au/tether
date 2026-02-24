package poly

import (
	"log/slog"
	"testing"
	"time"
)

func TestUpdateRefreshesLastActivity(t *testing.T) {
	mt := &mockTransport{events: []Event{}}
	sess := newTestSession(counterState{Count: 0}, mt)
	sess.logger = slog.Default()

	// Set lastActivity to the past so we can detect the change.
	past := time.Now().Add(-10 * time.Minute)
	sess.lastActivity = past

	sess.Update(func(s counterState) counterState {
		s.Count = 42
		return s
	})

	sess.mu.Lock()
	activity := sess.lastActivity
	sess.mu.Unlock()

	if !activity.After(past) {
		t.Errorf("expected lastActivity to be refreshed, still %v", activity)
	}
}
