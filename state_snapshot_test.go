package tether

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/jpl-au/tether/dev"
)

func TestConcurrentStateReadDoesNotWarn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log bytes.Buffer
		dev.Enable()
		dev.SetLogger(slog.New(slog.NewTextHandler(&log, nil)))
		defer dev.Reset()
		ct := newConnectedTransport()
		s := newTestSession(counterState{Count: 42}, ct)
		entered, release := make(chan struct{}), make(chan struct{})
		s.handle = func(_ Session, c counterState, _ Event) counterState { close(entered); <-release; c.Count++; return c }
		go s.run()
		s.cmds <- func() { s.exec(Event{}) }
		<-entered
		if got := s.State().Count; got != 42 {
			t.Errorf("in-flight snapshot = %d", got)
		}
		if strings.Contains(log.String(), "State() called during Handle") {
			t.Error("external State read incorrectly warned about Handle misuse")
		}
		close(release)
		synctest.Wait()
		if got := s.State().Count; got != 43 {
			t.Errorf("completed snapshot = %d", got)
		}
		s.stop()
		synctest.Wait()
	})
}
