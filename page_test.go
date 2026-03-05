package tether

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpl-au/fluent-tether/dev"
	"github.com/jpl-au/fluent-tether/event"
	"github.com/jpl-au/fluent/html5/div"
	"github.com/jpl-au/fluent/html5/span"
	"github.com/jpl-au/fluent/node"
)

// pageHandleCounter is a stateless handle function for testing.
func pageHandleCounter(sess Session, state counterState, ev Event) counterState {
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

func newTestPageHandler() http.Handler {
	return Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Render: renderCounter,
		Handle: pageHandleCounter,
	})
}

func TestPageGETRendersHTML(t *testing.T) {
	handler := newTestPageHandler()

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

func TestPageGETNoSessionAttribute(t *testing.T) {
	handler := newTestPageHandler()

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "data-tether-session") {
		t.Error("stateless page should not have data-tether-session attribute")
	}
}

func TestPageGETNoRetryDelayAttributes(t *testing.T) {
	handler := newTestPageHandler()

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

func TestPageGETWithOnNavigate(t *testing.T) {
	handler := Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Render: renderCounter,
		Handle: pageHandleCounter,
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

func TestPageGETDevMode(t *testing.T) {
	t.Cleanup(dev.Reset)

	handler := Page(PageConfig[counterState]{
		State:   func(r *http.Request) counterState { return counterState{} },
		Render:  renderCounter,
		Handle:  pageHandleCounter,
		DevMode: true,
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

func TestPagePOSTReturnsJSON(t *testing.T) {
	handler := newTestPageHandler()

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

func TestPagePOSTEventID(t *testing.T) {
	handler := newTestPageHandler()

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

func TestPagePOSTWithEffects(t *testing.T) {
	handler := newTestPageHandler()

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

func TestPagePOSTOriginCheck(t *testing.T) {
	handler := newTestPageHandler()

	req := httptest.NewRequest("POST", "http://myapp.com/app", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.com")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestPagePOSTOriginAllowed(t *testing.T) {
	handler := newTestPageHandler()

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

func TestPagePOSTInvalidJSON(t *testing.T) {
	handler := newTestPageHandler()

	req := httptest.NewRequest("POST", "/app", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPageMethodNotAllowed(t *testing.T) {
	handler := newTestPageHandler()

	req := httptest.NewRequest("PUT", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestPagePOSTPanicRecovery(t *testing.T) {
	handler := newTestPageHandler()

	body := `{"type":"click","action":"panic","data":{},"event_id":"1"}`
	req := httptest.NewRequest("POST", "/app", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestPageGETPanicRecovery(t *testing.T) {
	handler := Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { panic("render panic") },
		Render: renderCounter,
		Handle: pageHandleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestPageServesClientJS(t *testing.T) {
	handler := newTestPageHandler()

	req := httptest.NewRequest("GET", "/_tether/fluent-tether.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "fluent-tether") {
		t.Error("expected fluent-tether.js content")
	}
}

func TestPagePanicsOnMissingState(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing State")
		}
	}()
	Page(PageConfig[counterState]{
		Render: renderCounter,
		Handle: pageHandleCounter,
	})
}

func TestPagePanicsOnMissingRender(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing Render")
		}
	}()
	Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Handle: pageHandleCounter,
	})
}

func TestPagePanicsOnMissingHandle(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing Handle")
		}
	}()
	Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Render: renderCounter,
	})
}

func TestPagePOSTWithOnNavigate(t *testing.T) {
	handler := Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Render: renderCounter,
		Handle: pageHandleCounter,
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

func TestPageDevModeFromEnv(t *testing.T) {
	t.Setenv("TETHER_DEV", "1")
	t.Cleanup(dev.Reset)

	handler := Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Render: renderCounter,
		Handle: pageHandleCounter,
	})

	req := httptest.NewRequest("GET", "/app", nil)
	w := httptest.NewRecorder()
	handler.(*pageHandler[counterState]).ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "data-tether-dev") {
		t.Error("expected data-tether-dev from TETHER_DEV env var")
	}
}

func TestPagePOSTNavigateSkipsHandle(t *testing.T) {
	handler := Page(PageConfig[counterState]{
		State:  func(r *http.Request) counterState { return counterState{} },
		Render: renderCounter,
		Handle: func(_ Session, state counterState, _ Event) counterState {
			// Handle should NOT run for navigate events when
			// OnNavigate is set — this matches live session behaviour.
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

func TestPagePOSTComponentsDispatch(t *testing.T) {
	type pageState struct {
		Widget testWidget
		Other  string
	}

	handler := Page(PageConfig[pageState]{
		State: func(r *http.Request) pageState { return pageState{} },
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

	// Component event — should be routed to widget, not Handle.
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

	// Non-component event — should fall through to Handle.
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

// Verify the event.Type import compiles (used for event constants in
// tests above via string literals, but callers use event.Click etc.).
var _ event.Type = event.Click
