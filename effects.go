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
// effects (Flash, Signals) merge by key, and Prefetch accumulates
// (every hinted URL is kept). Effects that must all be delivered
// should be distinct signals or separate events.
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
	Prefetch []string // likely-next URLs to hint to the browser
}

// fxFrom recovers the effect fields of an update. send() reaches the
// no-transport case holding a [wire.Update] rather than the [Effects]
// it was built from, and the buffer that outlives the disconnect is
// keyed on Effects. Patches and morphs are deliberately not carried
// over: the reattach diff regenerates them from the current state.
func fxFrom(u wire.Update) *Effects {
	return &Effects{
		Announce: u.Announce,
		Flash:    u.Flash,
		Signals:  u.Signals,
		Toast:    u.Toast,
		Title:    u.Title,
		URL:      u.URL,
		Replace:  u.Replace,
		ScrollTo: u.ScrollTo,
		Download: u.Download,
		Prefetch: u.Prefetch,
	}
}

// Any reports whether any side effects have been buffered.
func (fx *Effects) Any() bool {
	return fx.Announce != "" || fx.Flash != nil || fx.Signals != nil ||
		fx.Toast != "" || fx.Title != "" || fx.URL != "" || fx.ScrollTo != "" ||
		fx.Download != "" || fx.Prefetch != nil
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
	if fx.Prefetch != nil {
		dst.Prefetch = append(dst.Prefetch, fx.Prefetch...)
	}
}

// mergeBounded merges src into fx like [Effects.copyInto], but refuses
// Flash and Signals keys once the two maps together hold limit distinct
// keys. It returns how many keys were refused so the caller can report
// them; updates to keys already held always apply, so a page with a
// fixed set of signals is never affected however long it merges.
//
// The bound exists because the disconnect window has no natural end: a
// handler that mints a fresh key per event would otherwise grow these
// maps for the whole reconnect timeout. src is consumed - it is always
// a freshly built, loop-local Effects - so refused keys are simply left
// behind.
func (fx *Effects) mergeBounded(src *Effects, limit int) int {
	held := len(fx.Flash) + len(fx.Signals)
	refused := 0

	if src.Flash != nil && fx.Flash == nil {
		fx.Flash = make(map[string]string, len(src.Flash))
	}
	for k, v := range src.Flash {
		if _, ok := fx.Flash[k]; !ok {
			if held >= limit {
				refused++
				continue
			}
			held++
		}
		fx.Flash[k] = v
	}

	if src.Signals != nil && fx.Signals == nil {
		fx.Signals = make(map[string]any, len(src.Signals))
	}
	for k, v := range src.Signals {
		if _, ok := fx.Signals[k]; !ok {
			if held >= limit {
				refused++
				continue
			}
			held++
		}
		fx.Signals[k] = v
	}

	// Scalars are last-write-wins and cost nothing to keep.
	if src.Announce != "" {
		fx.Announce = src.Announce
	}
	if src.Toast != "" {
		fx.Toast = src.Toast
	}
	if src.Title != "" {
		fx.Title = src.Title
	}
	if src.URL != "" {
		fx.URL = src.URL
		fx.Replace = src.Replace
	}
	return refused
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
	if fx.Prefetch != nil {
		u.Prefetch = fx.Prefetch
	}
}
