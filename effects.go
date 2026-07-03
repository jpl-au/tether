package tether

import (
	"maps"

	"github.com/jpl-au/tether/wire"
)

// Effects accumulates side effects during an exec cycle. Session
// methods (Toast, Navigate, Signal, etc.) populate these fields when
// called inside Handle. After Handle returns, the effects are flushed
// into the same Update message as the state diff so the client
// receives everything atomically in one frame.
//
// In stateful mode the framework manages Effects internally. In testing
// and pre-warming, [CaptureSession] exposes Effects directly so
// callers can read the buffered side effects after Handle returns.
//
// Scalar effects (Toast, Announce, ScrollTo, Download, Title, URL)
// are last-write-wins: when several Updates coalesce into one render
// cycle, only the final value of each scalar reaches the client. Map
// effects (Flash, Signals) merge by key. Effects that must all be
// delivered should be distinct signals or separate events.
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

// copyInto merges the buffered effects into dst - set scalar fields
// overwrite, map fields are merged key by key. Used to replay effects
// captured during pre-warming (OnNavigate on the initial GET) into
// the live session's first effect cycle.
func (fx *Effects) copyInto(dst *Effects) {
	if fx.Announce != "" {
		dst.Announce = fx.Announce
	}
	if fx.Flash != nil {
		if dst.Flash == nil {
			dst.Flash = make(map[string]string, len(fx.Flash))
		}
		maps.Copy(dst.Flash, fx.Flash)
	}
	if fx.Signals != nil {
		if dst.Signals == nil {
			dst.Signals = make(map[string]any, len(fx.Signals))
		}
		maps.Copy(dst.Signals, fx.Signals)
	}
	if fx.Toast != "" {
		dst.Toast = fx.Toast
	}
	if fx.Title != "" {
		dst.Title = fx.Title
	}
	if fx.URL != "" {
		dst.URL = fx.URL
		dst.Replace = fx.Replace
	}
	if fx.ScrollTo != "" {
		dst.ScrollTo = fx.ScrollTo
	}
	if fx.Download != "" {
		dst.Download = fx.Download
	}
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
