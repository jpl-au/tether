package poly

// effects accumulates side effects during an exec cycle. Session
// methods (Toast, Navigate, Signal, etc.) populate these fields when
// called inside Handle. After Handle returns, the effects are flushed
// into the same Update message as the state diff so the client
// receives everything atomically in one frame.
type effects struct {
	announce string
	flash    map[string]string
	signals  map[string]any
	toast    string
	title    string
	url      string
	replace  bool // true for replaceState, false for pushState
}

// any reports whether any side effects have been buffered.
func (fx *effects) any() bool {
	return fx.announce != "" || fx.flash != nil || fx.signals != nil ||
		fx.toast != "" || fx.title != "" || fx.url != ""
}

// merge copies buffered effects into an Update message.
func (fx *effects) merge(u *Update) {
	if fx.announce != "" {
		u.Announce = fx.announce
	}
	if fx.flash != nil {
		u.Flash = fx.flash
	}
	if fx.signals != nil {
		u.Signals = fx.signals
	}
	if fx.toast != "" {
		u.Toast = fx.toast
	}
	if fx.title != "" {
		u.Title = fx.title
	}
	if fx.url != "" {
		u.URL = fx.url
		u.Replace = fx.replace
	}
}
