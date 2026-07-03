package tether

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/wire"
)

// statelessHandleCounter is a stateless handle function for testing.
func statelessHandleCounter(sess Session, state counterState, ev Event) counterState {
	switch ev.Action {
	case "increment":
		state.Count++
	case "decrement":
		state.Count--
	case "toast":
		sess.Toast("hello")
	case "navigate":
		sess.Navigate("/other")
	case "title":
		sess.SetTitle("New Title")
	case "panic":
		panic("test panic")
	}
	return state
}

func newTestStatelessHandler() http.Handler {
	return Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       statelessHandleCounter,
	})
}

func TestStatelessGETRendersHTML(t *testing.T) {
	handler := newTestStatelessHandler()

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "data-tether-root") {
		t.Error("expected data-tether-root attribute")
	}
	if !strings.Contains(body, `data-tether-transport="fetch"`) {
		t.Error("expected data-tether-transport=fetch")
	}
	if !strings.Contains(body, "Count: 0") {
		t.Error("expected rendered content")
	}
}

func TestStatelessGETNoSessionAttribute(t *testing.T) {
	handler := newTestStatelessHandler()

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "data-tether-session") {
		t.Error("stateless page should not have data-tether-session attribute")
	}
}

func TestStatelessGETNoRetryDelayAttributes(t *testing.T) {
	handler := newTestStatelessHandler()

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "data-tether-retry-delay") {
		t.Error("stateless page should not have retry-delay attributes")
	}
	if strings.Contains(body, "data-tether-max-retry-delay") {
		t.Error("stateless page should not have max-retry-delay attributes")
	}
}

func TestStatelessGETWithOnNavigate(t *testing.T) {
	handler := Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       statelessHandleCounter,
		OnNavigate: func(_ Session, state counterState, p Params) counterState {
			if p.Query.Get("count") == "5" {
				state.Count = 5
			}
			return state
		},
	})

	req := httptest.NewRequest("GET", "/app?count=5", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "Count: 5") {
		t.Error("expected OnNavigate to set count to 5")
	}
}

func TestStatelessGETDevMode(t *testing.T) {
	t.Cleanup(dev.Reset)

	handler := Stateless(App{DevMode: true}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       statelessHandleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), "no-store")
	}
	if !strings.Contains(w.Body.String(), "data-tether-dev") {
		t.Error("expected data-tether-dev attribute")
	}
}

func TestStatelessPOSTReturnsJSON(t *testing.T) {
	handler := newTestStatelessHandler()

	body := `{"type":"click","action":"increment","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var msg testMessage
	if err := json.Unmarshal(w.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if msg.Type != "update" {
		t.Errorf("type = %q, want %q", msg.Type, "update")
	}
	if len(msg.Morphs) != 1 {
		t.Fatalf("morphs = %d, want 1", len(msg.Morphs))
	}
	if msg.Morphs[0].Key != "" {
		t.Errorf("morph key = %q, want empty (root)", msg.Morphs[0].Key)
	}
	if !strings.Contains(msg.Morphs[0].HTML, "Count: 1") {
		t.Errorf("morph HTML should contain Count: 1, got %s", msg.Morphs[0].HTML)
	}
}

func TestStatelessPOSTEventID(t *testing.T) {
	handler := newTestStatelessHandler()

	body := `{"type":"click","action":"increment","data":{},"event_id":"42"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	json.Unmarshal(w.Body.Bytes(), &msg)
	if msg.EventID != "42" {
		t.Errorf("event_id = %q, want %q", msg.EventID, "42")
	}
}

func TestStatelessPOSTWithEffects(t *testing.T) {
	handler := newTestStatelessHandler()

	t.Run("toast", func(t *testing.T) {
		body := `{"type":"click","action":"toast","data":{},"event_id":"1"}`
		req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		var msg testMessage
		json.Unmarshal(w.Body.Bytes(), &msg)
		if msg.Toast != "hello" {
			t.Errorf("toast = %q, want %q", msg.Toast, "hello")
		}
	})

	t.Run("navigate", func(t *testing.T) {
		body := `{"type":"click","action":"navigate","data":{},"event_id":"2"}`
		req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		var msg testMessage
		json.Unmarshal(w.Body.Bytes(), &msg)
		if msg.URL != "/other" {
			t.Errorf("url = %q, want %q", msg.URL, "/other")
		}
	})

	t.Run("title", func(t *testing.T) {
		body := `{"type":"click","action":"title","data":{},"event_id":"3"}`
		req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		var msg testMessage
		json.Unmarshal(w.Body.Bytes(), &msg)
		if msg.Title != "New Title" {
			t.Errorf("title = %q, want %q", msg.Title, "New Title")
		}
	})
}

func TestStatelessPOSTOriginCheck(t *testing.T) {
	handler := newTestStatelessHandler()

	req := httptest.NewRequest("POST", "http://myapp.com/app", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.com")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestStatelessPOSTOriginAllowed(t *testing.T) {
	handler := newTestStatelessHandler()

	body := `{"type":"click","action":"increment","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "http://myapp.com/app", strings.NewReader(body))
	req.Header.Set("Origin", "http://myapp.com")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestStatelessPOSTInvalidJSON(t *testing.T) {
	handler := newTestStatelessHandler()

	req := httptest.NewRequest("POST", "/app", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestStatelessMethodNotAllowed(t *testing.T) {
	handler := newTestStatelessHandler()

	req := httptest.NewRequest("PUT", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestStatelessPOSTPanicRecovery(t *testing.T) {
	handler := newTestStatelessHandler()

	body := `{"type":"click","action":"panic","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestStatelessGETPanicRecovery(t *testing.T) {
	handler := Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { panic("render panic") },
		Render:       renderCounter,
		Handle:       statelessHandleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestStatelessServesClientJS(t *testing.T) {
	handler := newTestStatelessHandler()

	req := httptest.NewRequest("GET", "/_tether/tether.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "tether") {
		t.Error("expected tether.js content")
	}
}

func TestStatelessPanicsOnMissingState(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing State")
		}
	}()
	Stateless(App{}, StatelessConfig[counterState]{
		Render: renderCounter,
		Handle: statelessHandleCounter,
	})
}

func TestStatelessPanicsOnMissingRender(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing Render")
		}
	}()
	Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Handle:       statelessHandleCounter,
	})
}

func TestStatelessPanicsOnMissingHandle(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing Handle")
		}
	}()
	Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
	})
}

func TestStatelessPOSTWithOnNavigate(t *testing.T) {
	handler := Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       statelessHandleCounter,
		OnNavigate: func(_ Session, state counterState, p Params) counterState {
			if p.Query.Get("count") == "5" {
				state.Count = 5
			}
			return state
		},
	})

	// POST with query param sets initial count, then increment adds 1.
	body := `{"type":"click","action":"increment","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app?count=5", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	json.Unmarshal(w.Body.Bytes(), &msg)
	if len(msg.Morphs) != 1 {
		t.Fatalf("morphs = %d, want 1", len(msg.Morphs))
	}
	if !strings.Contains(msg.Morphs[0].HTML, "Count: 6") {
		t.Errorf("expected Count: 6 (5 from params + 1 from increment), got %s", msg.Morphs[0].HTML)
	}
}

func TestStatelessDevModeFromEnv(t *testing.T) {
	t.Setenv("TETHER_DEV", "1")
	t.Cleanup(dev.Reset)

	handler := Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       statelessHandleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.(*statelessHandler[counterState]).ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "data-tether-dev") {
		t.Error("expected data-tether-dev from TETHER_DEV env var")
	}
}

func TestStatelessPOSTNavigateSkipsHandle(t *testing.T) {
	handler := Stateless(App{}, StatelessConfig[counterState]{
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle: func(_ Session, state counterState, _ Event) counterState {
			// Handle should NOT run for navigate events when
			// OnNavigate is set - this matches live session behaviour.
			state.Count = 999
			return state
		},
		OnNavigate: func(_ Session, state counterState, p Params) counterState {
			if p.Query.Get("count") == "5" {
				state.Count = 5
			}
			return state
		},
	})

	body := `{"type":"navigate","action":"","data":{"path":"/app","search":"count=5"},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	json.Unmarshal(w.Body.Bytes(), &msg)
	if len(msg.Morphs) != 1 {
		t.Fatalf("morphs = %d, want 1", len(msg.Morphs))
	}
	// Should be 5 from OnNavigate, NOT 999 from Handle.
	if !strings.Contains(msg.Morphs[0].HTML, "Count: 5") {
		t.Errorf("expected Count: 5, got %s", msg.Morphs[0].HTML)
	}
}

func TestStatelessPOSTComponentsDispatch(t *testing.T) {
	type pageState struct {
		Widget testWidget
		Other  string
	}

	handler := Stateless(App{}, StatelessConfig[pageState]{
		InitialState: func(r *http.Request) pageState { return pageState{} },
		Render: func(s pageState) node.Node {
			return div.New(
				span.Textf("count:%d", s.Widget.Count).Dynamic("count"),
				span.Textf("other:%s", s.Other).Dynamic("other"),
			)
		},
		Handle: func(_ Session, s pageState, ev Event) pageState {
			if ev.Action == "set-other" {
				s.Other = ev.Value()
			}
			return s
		},
		Components: []ComponentMount[pageState]{
			Mount("widget",
				func(s pageState) testWidget { return s.Widget },
				func(s pageState, w testWidget) pageState { s.Widget = w; return s },
			),
		},
	})

	// Component event - should be routed to widget, not Handle.
	body := `{"type":"click","action":"widget.inc","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	json.Unmarshal(w.Body.Bytes(), &msg)
	if len(msg.Morphs) != 1 {
		t.Fatalf("morphs = %d, want 1", len(msg.Morphs))
	}
	if !strings.Contains(msg.Morphs[0].HTML, "count:1") {
		t.Errorf("expected count:1 from component dispatch, got %s", msg.Morphs[0].HTML)
	}

	// Non-component event - should fall through to Handle.
	body = `{"type":"click","action":"set-other","data":{"value":"hi"},"event_id":"2"}`
	req = httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &msg)
	if !strings.Contains(msg.Morphs[0].HTML, "other:hi") {
		t.Errorf("expected other:hi from Handle fallthrough, got %s", msg.Morphs[0].HTML)
	}
}

func TestStatelessPOSTMorphTargetedKeys(t *testing.T) {
	type pageState struct {
		Count int
		Title string
	}

	handler := Stateless(App{}, StatelessConfig[pageState]{
		InitialState: func(r *http.Request) pageState { return pageState{Title: "hello"} },
		Render: func(s pageState) node.Node {
			return div.New(
				span.Textf("Count: %d", s.Count).Dynamic("count"),
				span.Textf("Title: %s", s.Title).Dynamic("title"),
			)
		},
		Handle: func(sess Session, s pageState, ev Event) pageState {
			if ev.Action == "increment" {
				s.Count++
				sess.Morph("count")
			}
			return s
		},
	})

	body := `{"type":"click","action":"increment","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	if err := json.Unmarshal(w.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(msg.Morphs) != 1 {
		t.Fatalf("morphs = %d, want 1", len(msg.Morphs))
	}
	if msg.Morphs[0].Key != "count" {
		t.Errorf("morph key = %q, want %q", msg.Morphs[0].Key, "count")
	}
	if !strings.Contains(msg.Morphs[0].HTML, "Count: 1") {
		t.Errorf("morph HTML should contain Count: 1, got %s", msg.Morphs[0].HTML)
	}
}

func TestStatelessPOSTMorphMultipleKeys(t *testing.T) {
	type pageState struct {
		Count int
		Title string
	}

	handler := Stateless(App{}, StatelessConfig[pageState]{
		InitialState: func(r *http.Request) pageState { return pageState{} },
		Render: func(s pageState) node.Node {
			return div.New(
				span.Textf("Count: %d", s.Count).Dynamic("count"),
				span.Textf("Title: %s", s.Title).Dynamic("title"),
			)
		},
		Handle: func(sess Session, s pageState, ev Event) pageState {
			if ev.Action == "update-both" {
				s.Count++
				s.Title = "updated"
				sess.Morph("count", "title")
			}
			return s
		},
	})

	body := `{"type":"click","action":"update-both","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	if err := json.Unmarshal(w.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(msg.Morphs) != 2 {
		t.Fatalf("morphs = %d, want 2", len(msg.Morphs))
	}

	// Collect morphs by key for order-independent assertions.
	byKey := make(map[string]string, len(msg.Morphs))
	for _, m := range msg.Morphs {
		byKey[m.Key] = m.HTML
	}
	if !strings.Contains(byKey["count"], "Count: 1") {
		t.Errorf("count morph should contain Count: 1, got %s", byKey["count"])
	}
	if !strings.Contains(byKey["title"], "Title: updated") {
		t.Errorf("title morph should contain Title: updated, got %s", byKey["title"])
	}
}

func TestStatelessPOSTMorphFallsBackToRoot(t *testing.T) {
	handler := newTestStatelessHandler()

	// No Morph call in handle - should fall back to root morph.
	body := `{"type":"click","action":"increment","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	json.Unmarshal(w.Body.Bytes(), &msg)
	if len(msg.Morphs) != 1 {
		t.Fatalf("morphs = %d, want 1", len(msg.Morphs))
	}
	if msg.Morphs[0].Key != "" {
		t.Errorf("morph key = %q, want empty (root)", msg.Morphs[0].Key)
	}
}

func TestStatelessPOSTMorphWithEffects(t *testing.T) {
	type pageState struct {
		Count int
	}

	handler := Stateless(App{}, StatelessConfig[pageState]{
		InitialState: func(r *http.Request) pageState { return pageState{} },
		Render: func(s pageState) node.Node {
			return div.New(
				span.Textf("Count: %d", s.Count).Dynamic("count"),
			)
		},
		Handle: func(sess Session, s pageState, ev Event) pageState {
			s.Count++
			sess.Morph("count")
			sess.Toast("incremented")
			return s
		},
	})

	body := `{"type":"click","action":"increment","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var msg testMessage
	json.Unmarshal(w.Body.Bytes(), &msg)
	if len(msg.Morphs) != 1 {
		t.Fatalf("morphs = %d, want 1", len(msg.Morphs))
	}
	if msg.Morphs[0].Key != "count" {
		t.Errorf("morph key = %q, want %q", msg.Morphs[0].Key, "count")
	}
	if msg.Toast != "incremented" {
		t.Errorf("toast = %q, want %q", msg.Toast, "incremented")
	}
}

// Verify the event.Type import compiles (used for event constants in
// tests above via string literals, but callers use event.Click etc.).
var _ event.Type = event.Click

// --- HTML wire format ---

func newHTMLWireHandler(morphKeys ...string) http.Handler {
	return Stateless(App{}, StatelessConfig[counterState]{
		WireFormat:   wire.HTML,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle: func(sess Session, state counterState, ev Event) counterState {
			if len(morphKeys) > 0 {
				sess.Morph(morphKeys...)
			}
			return statelessHandleCounter(sess, state, ev)
		},
	})
}

func postEvent(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestStatelessHTMLWireRootMorph verifies that wire.HTML answers a
// POST event with plain HTML - the rendered tree as the body, no
// JSON envelope - so responses are curl-inspectable.
func TestStatelessHTMLWireRootMorph(t *testing.T) {
	h := newHTMLWireHandler()
	w := postEvent(t, h, `{"type":"click","action":"increment","data":{},"event_id":"1"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if w.Header().Get("Tether-Morph") != "" {
		t.Error("root morph response must not carry the keyed header")
	}
	body := w.Body.String()
	if !strings.Contains(body, "Count: 1") {
		t.Errorf("body should contain the rendered tree, got %s", body)
	}
	if strings.Contains(body, `"type":"update"`) {
		t.Error("body should be plain HTML, not a JSON envelope")
	}
	if strings.Contains(body, "data-tether-effects") {
		t.Error("no effects were raised - the island should be omitted")
	}
}

// TestStatelessHTMLWireKeyedFragments verifies that sess.Morph
// produces targeted keyed fragments flagged by the Tether-Morph
// header.
func TestStatelessHTMLWireKeyedFragments(t *testing.T) {
	h := newHTMLWireHandler("count")
	w := postEvent(t, h, `{"type":"click","action":"increment","data":{},"event_id":"1"}`)

	if w.Header().Get("Tether-Morph") != "keyed" {
		t.Fatalf("Tether-Morph = %q, want keyed", w.Header().Get("Tether-Morph"))
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-tether-key="count"`) {
		t.Errorf("fragment should carry its key attribute, got %s", body)
	}
	if strings.Contains(body, "<div") {
		t.Error("keyed response should contain only the fragment, not the full tree")
	}
}

// TestStatelessHTMLWireEffectsIsland verifies that side effects ride
// in the JSON template island using the standard wire field names.
func TestStatelessHTMLWireEffectsIsland(t *testing.T) {
	h := newHTMLWireHandler()
	w := postEvent(t, h, `{"type":"click","action":"toast","data":{},"event_id":"1"}`)

	body := w.Body.String()
	idx := strings.Index(body, `<template data-tether-effects>`)
	if idx == -1 {
		t.Fatalf("expected effects island, got %s", body)
	}
	end := strings.Index(body, "</template>")
	raw := body[idx+len(`<template data-tether-effects>`) : end]

	var fx map[string]any
	if err := json.Unmarshal([]byte(raw), &fx); err != nil {
		t.Fatalf("island is not valid JSON: %v (%s)", err, raw)
	}
	if fx["toast"] != "hello" {
		t.Errorf("toast = %v, want hello", fx["toast"])
	}
}

// TestStatelessCacheControl verifies the configurable GET cache
// header: stateless pages carry no session token, so caching is the
// developer's call.
func TestStatelessCacheControl(t *testing.T) {
	h := Stateless(App{}, StatelessConfig[counterState]{
		CacheControl: "public, max-age=60",
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       statelessHandleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want the configured value", cc)
	}
}

// TestStatefulRejectsHTMLWireFormat verifies the loud failure: the
// HTML wire format cannot ride a stateful transport.
func TestStatefulRejectsHTMLWireFormat(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Stateful should panic on wire.HTML")
		}
	}()
	Stateful(App{}, StatefulConfig[counterState]{
		WireFormat:   wire.HTML,
		Mode:         mode.WebSocket,
		Upgrade:      stubUpgrade,
		InitialState: func(r *http.Request) counterState { return counterState{} },
		Render:       renderCounter,
		Handle:       handleCounter,
	})
}
