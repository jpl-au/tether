package poly

// HandleFunc processes a client event and returns a [HandleResult]
// containing the new state and optional side effects (announce, flash,
// title, URL changes). Side effects are merged into the same update
// message as the state diff, so the client receives everything
// atomically in a single frame.
//
// The session parameter is provided for identification (e.g.
// [Session.ID] for [Group.BroadcastOthers]) and for passing to
// external systems. Do not call state-mutating Session methods
// (Update, Navigate, SetTitle, Announce, Flash, Close) from within
// Handle — the session mutex is held and these will deadlock. Use
// HandleResult's With* methods to return side effects instead.
//
// The function should treat the input state as immutable — return a new
// value with the desired changes. Returning the original state
// unchanged is valid and will produce no diff (especially when an
// Equal function is configured).
//
// Use [Result] to create a HandleResult from a bare state value when
// no side effects are needed.
type HandleFunc[S any] func(session *Session[S], state S, event Event) HandleResult[S]

// HandleResult wraps the new state with optional side effects that
// the session applies in the same update message as the state diff.
// Use [Result] to create one from a bare state, and the With* methods
// to attach side effects.
type HandleResult[S any] struct {
	State    S
	Announce string            // text for aria-live region
	Flash    map[string]string // CSS selector → text, cleared after 5s
	Toast    string            // global notification text, cleared after 5s
	Title    string            // set document.title
	URL      string            // push/replace browser URL
	Replace  bool              // true for replaceState, false for pushState
}

// Result creates a [HandleResult] from a bare state value. This is the
// common case when no side effects are needed.
func Result[S any](state S) HandleResult[S] {
	return HandleResult[S]{State: state}
}

// WithAnnounce attaches a screen reader announcement to the result.
func (r HandleResult[S]) WithAnnounce(text string) HandleResult[S] {
	r.Announce = text
	return r
}

// WithFlash attaches a flash notification to the result. The selector
// identifies the target element; the text is displayed for 5 seconds.
func (r HandleResult[S]) WithFlash(selector, text string) HandleResult[S] {
	if r.Flash == nil {
		r.Flash = make(map[string]string)
	}
	r.Flash[selector] = text
	return r
}

// WithToast attaches a global notification to the result. The client
// JS handles displaying the toast in a transient overlay.
func (r HandleResult[S]) WithToast(text string) HandleResult[S] {
	r.Toast = text
	return r
}

// WithTitle sets the browser's document title.
func (r HandleResult[S]) WithTitle(title string) HandleResult[S] {
	r.Title = title
	return r
}

// WithNavigate pushes a URL change with a history entry.
func (r HandleResult[S]) WithNavigate(rawURL string) HandleResult[S] {
	r.URL = rawURL
	r.Replace = false
	return r
}

// WithReplaceURL updates the browser URL without a history entry.
func (r HandleResult[S]) WithReplaceURL(rawURL string) HandleResult[S] {
	r.URL = rawURL
	r.Replace = true
	return r
}

// hasEffects reports whether a HandleResult carries any side effects
// (announce, flash, title, or URL changes).
func hasEffects[S any](effects *HandleResult[S]) bool {
	if effects == nil {
		return false
	}
	return effects.Announce != "" || effects.Flash != nil ||
		effects.Toast != "" || effects.Title != "" || effects.URL != ""
}

// mergeEffects copies side effect fields from a HandleResult into an
// Update. Called by applyState to combine diff output with Handle's
// side effects into a single wire message.
func mergeEffects[S any](update *Update, effects *HandleResult[S]) {
	if effects == nil {
		return
	}
	if effects.Announce != "" {
		update.Announce = effects.Announce
	}
	if effects.Flash != nil {
		update.Flash = effects.Flash
	}
	if effects.Toast != "" {
		update.Toast = effects.Toast
	}
	if effects.Title != "" {
		update.Title = effects.Title
	}
	if effects.URL != "" {
		update.URL = effects.URL
		update.Replace = effects.Replace
	}
}
