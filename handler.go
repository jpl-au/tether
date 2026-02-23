package poly

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	jit "github.com/jpl-au/fluent-jit"
)

// Config configures a poly handler. The type parameter S is the session
// state — it can be any type (struct, int, string, etc.).
type Config[S any] struct {
	// Upgrade converts an HTTP request into a Transport connection.
	// Use ws.Upgrade for WebSocket connections.
	Upgrade func(w http.ResponseWriter, r *http.Request) (Transport, error)

	// InitialState returns the starting state for a new session.
	// Called once per connection to create the initial state.
	InitialState func(r *http.Request) S

	// Render builds a node tree from the current state.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state.
	Handle HandleFunc[S]

	// OnConnect is called when a session is established. Optional.
	OnConnect func(session *Session[S])

	// OnDisconnect is called when a session ends. Optional.
	OnDisconnect func(session *Session[S])

	// Equal compares two states. If provided and returns true, the diff
	// is skipped entirely for that event. Optional.
	Equal func(a, b S) bool

	// Logger is used for session errors. Defaults to slog.Default().
	Logger *slog.Logger
}

// New creates an http.Handler that manages poly sessions.
//
// GET requests receive the initial HTML page with the client JS injected.
// Requests with an Upgrade header start a session event loop.
func New[S any](cfg Config[S]) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &handler[S]{cfg: cfg}
}

type handler[S any] struct {
	cfg Config[S]
}

func (h *handler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") == "websocket" {
		h.serveSession(w, r)
		return
	}

	h.serveInitialPage(w, r)
}

// serveInitialPage renders the full HTML and injects the client runtime.
func (h *handler[S]) serveInitialPage(w http.ResponseWriter, r *http.Request) {
	state := h.cfg.InitialState(r)
	tree := h.cfg.Render(state)

	// Use a differ to do the initial render so snapshots are captured,
	// but we don't store this differ — the session creates its own.
	differ := jit.NewDiffer()
	html := differ.Render(tree)

	var buf bytes.Buffer
	buf.WriteString(`<div data-poly-root data-poly-endpoint="`)
	buf.WriteString(r.URL.Path)
	buf.WriteString(`">`)
	buf.Write(html)
	buf.WriteString("</div>\n<script src=\"/_poly/idiomorph.min.js\"></script>\n<script src=\"/_poly/fluent-poly.js\"></script>\n")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// serveSession upgrades the connection and runs the session event loop.
func (h *handler[S]) serveSession(w http.ResponseWriter, r *http.Request) {
	transport, err := h.cfg.Upgrade(w, r)
	if err != nil {
		http.Error(w, "connection upgrade failed", http.StatusInternalServerError)
		return
	}

	state := h.cfg.InitialState(r)
	differ := jit.NewDiffer()

	sess := &Session[S]{
		id:        newID(),
		state:     state,
		render:    h.cfg.Render,
		handle:    h.cfg.Handle,
		differ:    differ,
		transport: transport,
		logger:    h.cfg.Logger,
	}

	if h.cfg.Equal != nil {
		sess.equal = h.cfg.Equal
	}
	if h.cfg.OnDisconnect != nil {
		fn := h.cfg.OnDisconnect
		sess.onDisconnect = func() { fn(sess) }
	}

	// Seed the differ with the initial render so the first diff has
	// a baseline to compare against.
	tree := sess.render(sess.state)
	differ.Render(tree)

	if h.cfg.OnConnect != nil {
		h.cfg.OnConnect(sess)
	}

	// Block on the event loop — this goroutine is owned by the HTTP server
	sess.run()
}

// ServeClient returns an http.Handler that serves the embedded client JS files.
// Mount this at /_poly/ to serve fluent-poly.js and idiomorph.
func ServeClient() http.Handler {
	return http.FileServer(http.FS(clientFiles()))
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
