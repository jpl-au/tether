package tether

import "github.com/jpl-au/tether/wire"

// Effects accumulates side effects during an exec cycle. Session
// methods (Toast, Navigate, Signal, etc.) populate these fields when
// called inside Handle. After Handle returns, the effects are flushed
// into the same Update message as the state diff so the client
// receives everything atomically in one frame.
//
// In stateful mode the framework manages Effects internally. In testing
// and pre-warming, [CaptureSession] exposes Effects directly so
// callers can read the buffered side effects after Handle returns.
type Effects struct {
	Announce string
	Flash    map[string]string
	Signals  map[string]any
	Toast    string
	Title    string
	URL      string
	Replace  bool // true for replaceState, false for pushState
	ScrollTo string
	Download string
}

// Any reports whether any side effects have been buffered.
func (fx *Effects) Any() bool {
	return fx.Announce != "" || fx.Flash != nil || fx.Signals != nil ||
		fx.Toast != "" || fx.Title != "" || fx.URL != "" || fx.ScrollTo != "" ||
		fx.Download != ""
}

// merge copies buffered effects into an update message.
func (fx *Effects) merge(u *wire.Update) {
	if fx.Announce != "" {
		u.Announce = fx.Announce
	}
	if fx.Flash != nil {
		u.Flash = fx.Flash
	}
	if fx.Signals != nil {
		u.Signals = fx.Signals
	}
	if fx.Toast != "" {
		u.Toast = fx.Toast
	}
	if fx.Title != "" {
		u.Title = fx.Title
	}
	if fx.URL != "" {
		u.URL = fx.URL
		u.Replace = fx.Replace
	}
	if fx.ScrollTo != "" {
		u.ScrollTo = fx.ScrollTo
	}
	if fx.Download != "" {
		u.Download = fx.Download
	}
}
