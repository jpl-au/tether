package wire

// Encoder serialises an [Update] into bytes for transport. The session
// calls Encode after each render-diff cycle; the resulting bytes are
// passed directly to Transport.Send.
type Encoder interface {
	Encode(u Update) ([]byte, error)
}

// Update is the format-agnostic representation of changes to send to
// the client. A single update can carry any combination of content
// patches (targeted key replacements), structural morphs (DOM
// mutations applied via idiomorph), URL changes (pushState/
// replaceState), and side effects (signals, toasts, flashes).
// Combining them in one message lets the client apply everything
// atomically in a single pass.
type Update struct {
	Patches  []Patch
	Morphs   []Morph
	Session  string            // if non-empty, client must adopt this session ID
	URL      string            // if non-empty, push/replace browser URL
	Replace  bool              // true for replaceState, false for pushState
	Title    string            // if non-empty, set document.title
	Flash    map[string]string // key: CSS selector, value: plain text to display
	Signals  map[string]any    // key: signal name, value: pushed to bound elements
	Announce string            // if non-empty, inject into an aria-live region
	Toast    string            // if non-empty, show a global notification
	ScrollTo string            // if non-empty, scroll element into view
	Download string            // if non-empty, trigger a file download from this URL
	EventID  string            // echoed from the triggering Event for correlation

	// Hashes carries the complete current content-hash map for the
	// page's Dynamic fragments, keyed by Dynamic key. Sent by
	// stateless handlers with AutoFragments enabled; the client
	// replaces its stored map wholesale and echoes it with the next
	// event, so the protocol is self-healing - a missed update can
	// only cost one redundant fragment send, never a stale page.
	Hashes map[string]string
}

// Patch is a targeted content replacement. Key identifies a
// Dynamic-keyed element in the DOM; HTML replaces its innerHTML.
type Patch struct {
	Key  string
	HTML []byte
}

// Morph is a structural change applied via idiomorph. An empty Key
// targets the root element.
type Morph struct {
	Key  string
	HTML []byte
}
