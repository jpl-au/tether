package tether

import (
	"context"
	"testing"

	"github.com/jpl-au/tether/push"
)

func TestCaptureSessionID(t *testing.T) {
	cs := &CaptureSession{SessionID: "abc"}
	if cs.ID() != "abc" {
		t.Errorf("ID() = %q, want abc", cs.ID())
	}
}

func TestCaptureSessionIDEmpty(t *testing.T) {
	cs := &CaptureSession{}
	if cs.ID() != "" {
		t.Errorf("ID() = %q, want empty", cs.ID())
	}
}

func TestCaptureSessionContext(t *testing.T) {
	cs := &CaptureSession{}
	ctx := cs.Context()
	if ctx != context.Background() {
		t.Error("Context() should return context.Background()")
	}
}

func TestCaptureSessionToast(t *testing.T) {
	cs := &CaptureSession{}
	cs.Toast("hello")
	if cs.Effects.Toast != "hello" {
		t.Errorf("Toast = %q, want hello", cs.Effects.Toast)
	}
}

func TestCaptureSessionNavigate(t *testing.T) {
	cs := &CaptureSession{}
	cs.Navigate("/next")
	if cs.Effects.URL != "/next" {
		t.Errorf("URL = %q, want /next", cs.Effects.URL)
	}
	if cs.Effects.Replace {
		t.Error("Replace should be false after Navigate")
	}
}

func TestCaptureSessionReplaceURL(t *testing.T) {
	cs := &CaptureSession{}
	cs.ReplaceURL("/replaced")
	if cs.Effects.URL != "/replaced" {
		t.Errorf("URL = %q, want /replaced", cs.Effects.URL)
	}
	if !cs.Effects.Replace {
		t.Error("Replace should be true after ReplaceURL")
	}
}

func TestCaptureSessionSetTitle(t *testing.T) {
	cs := &CaptureSession{}
	cs.SetTitle("My Page")
	if cs.Effects.Title != "My Page" {
		t.Errorf("Title = %q, want My Page", cs.Effects.Title)
	}
}

func TestCaptureSessionAnnounce(t *testing.T) {
	cs := &CaptureSession{}
	cs.Announce("done")
	if cs.Effects.Announce != "done" {
		t.Errorf("Announce = %q, want done", cs.Effects.Announce)
	}
}

func TestCaptureSessionFlashLazyInit(t *testing.T) {
	cs := &CaptureSession{}
	cs.Flash("#msg", "saved")
	if cs.Effects.Flash == nil {
		t.Fatal("Flash map should be initialised")
	}
	if cs.Effects.Flash["#msg"] != "saved" {
		t.Errorf("Flash[#msg] = %q, want saved", cs.Effects.Flash["#msg"])
	}
}

func TestCaptureSessionFlashMultiple(t *testing.T) {
	cs := &CaptureSession{}
	cs.Flash("#a", "one")
	cs.Flash("#b", "two")
	if len(cs.Effects.Flash) != 2 {
		t.Errorf("Flash has %d entries, want 2", len(cs.Effects.Flash))
	}
}

func TestCaptureSessionSignalLazyInit(t *testing.T) {
	cs := &CaptureSession{}
	cs.Signal("count", 42)
	if cs.Effects.Signals == nil {
		t.Fatal("Signals map should be initialised")
	}
	if cs.Effects.Signals["count"] != 42 {
		t.Errorf("Signals[count] = %v, want 42", cs.Effects.Signals["count"])
	}
}

func TestCaptureSessionSignalsLazyInitAndMerge(t *testing.T) {
	cs := &CaptureSession{}
	cs.Signal("a", 1)
	cs.Signals(map[string]any{"b": 2, "c": 3})
	if len(cs.Effects.Signals) != 3 {
		t.Errorf("Signals has %d entries, want 3", len(cs.Effects.Signals))
	}
	if cs.Effects.Signals["b"] != 2 {
		t.Errorf("Signals[b] = %v, want 2", cs.Effects.Signals["b"])
	}
}

func TestCaptureSessionPushReturnsConfiguredError(t *testing.T) {
	cs := &CaptureSession{PushErr: ErrPushPreWarm}
	if err := cs.Push(push.Notification{}); err != ErrPushPreWarm {
		t.Errorf("Push() = %v, want ErrPushPreWarm", err)
	}
}

func TestCaptureSessionPushReturnsNilByDefault(t *testing.T) {
	cs := &CaptureSession{}
	if err := cs.Push(push.Notification{}); err != nil {
		t.Errorf("Push() = %v, want nil", err)
	}
}

func TestCaptureSessionCloseIsNoop(t *testing.T) {
	cs := &CaptureSession{}
	cs.Close() // should not panic
}

func TestCaptureSessionEnqueueRunsSynchronously(t *testing.T) {
	cs := &CaptureSession{}
	ran := false
	cs.enqueue(func() { ran = true })
	if !ran {
		t.Error("enqueue should execute fn synchronously")
	}
}

func TestCaptureSessionEmitterSessionID(t *testing.T) {
	cs := &CaptureSession{SessionID: "test-123"}
	if cs.sessionID() != "test-123" {
		t.Errorf("sessionID() = %q, want test-123", cs.sessionID())
	}
}

func TestCaptureSessionScrollTo(t *testing.T) {
	cs := &CaptureSession{}
	cs.ScrollTo("#card-5")
	if cs.Effects.ScrollTo != "#card-5" {
		t.Errorf("ScrollTo = %q, want #card-5", cs.Effects.ScrollTo)
	}
}

func TestCaptureSessionFreshEffects(t *testing.T) {
	// Verify a fresh CaptureSession has zero-value effects.
	cs := &CaptureSession{}
	if cs.Effects.Toast != "" || cs.Effects.URL != "" || cs.Effects.Title != "" ||
		cs.Effects.Announce != "" || cs.Effects.Flash != nil || cs.Effects.Signals != nil ||
		cs.Effects.ScrollTo != "" || cs.Effects.Replace {
		t.Error("fresh CaptureSession should have zero-value Effects")
	}
}
