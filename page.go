package poly

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jpl-au/fluent-poly/mode"
	"github.com/jpl-au/fluent/node"
)

// PageConfig wires together a stateless page: how to reconstruct state
// from each request, how to render it, and how to handle events. Unlike
// [Config], there is no persistent transport, no session pool, and no
// command loop — each request is independent. The server reconstructs
// state, renders HTML, and returns the response.
//
// GET requests render the full page. POST requests handle a client
// event, render the new state, and return a JSON update (the same wire
// format as live mode) with a root morph and any side effects.
//
// At minimum, set State, Render, and Handle. Everything else is
// optional and has sensible defaults.
type PageConfig[S any] struct {
	// State reconstructs the page state from the HTTP request. Called
	// on every request (GET and POST). Derive state from the URL,
	// cookies, headers, or a database — not from r.Body, which
	// contains the event JSON on POST requests.
	State func(r *http.Request) S

	// Render builds a node tree from the current state. Same type as
	// [Config].Render — a pure function that returns a Fluent node
	// tree. The same render function can be used for both live and
	// stateless pages.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state. Side
	// effects (toast, navigate, title, etc.) are expressed as calls on
	// the [PreSession] parameter — the same interface used by
	// [Config].HandleParams. The effects are included in the JSON
	// response so the client can apply them atomically.
	Handle func(session PreSession, state S, event Event) S

	// HandleParams processes URL parameters on every request. Called
	// after State on both GET and POST. Same signature as
	// [Config].HandleParams. Optional.
	HandleParams func(session PreSession, state S, params Params) S

	// Layout wraps the page content in a full HTML document. Runs on
	// every GET request (stateless pages reconstruct state each time).
	// Signal bindings in the Layout shell work document-wide — see
	// [Config].Layout for details. Optional.
	Layout func(state S, content node.Node) node.Node

	// AllowedOrigins restricts POST events to requests whose Origin
	// header matches one of these values. Same semantics as
	// [Config].AllowedOrigins. Optional.
	AllowedOrigins []string

	// MaxEventBytes limits the size of a POST event body. Zero
	// defaults to 64 KB.
	MaxEventBytes int64

	// DefaultDebounce is the debounce interval applied to input
	// events when the element does not specify data-poly-debounce.
	// Zero defaults to 300 milliseconds.
	DefaultDebounce time.Duration

	// TransitionTimeout is how long the client waits for a CSS
	// transitionend event before forcibly removing a leaving element.
	// Zero defaults to 5 seconds.
	TransitionTimeout time.Duration

	// DevMode sets Cache-Control: no-store and emits the
	// data-poly-dev attribute. Enable via this field or the POLY_DEV
	// environment variable.
	DevMode bool

	// Logger is used for handler errors. Defaults to slog.Default().
	Logger *slog.Logger
}

// Page creates an [http.Handler] for a stateless page. GET requests
// render the full HTML page. POST requests handle a client event and
// return a JSON update with the new HTML and any side effects.
//
// Unlike [New], there is no persistent connection — the client sends
// events via individual fetch POST requests and applies the response.
func Page[S any](cfg PageConfig[S]) http.Handler {
	if cfg.State == nil {
		panic("poly: PageConfig.State is required")
	}
	if cfg.Render == nil {
		panic("poly: PageConfig.Render is required")
	}
	if cfg.Handle == nil {
		panic("poly: PageConfig.Handle is required")
	}

	if !cfg.DevMode && os.Getenv("POLY_DEV") != "" {
		cfg.DevMode = true
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxEventBytes == 0 {
		cfg.MaxEventBytes = defaultMaxEventBytes
	}
	if cfg.DefaultDebounce == 0 {
		cfg.DefaultDebounce = defaultDefaultDebounce
	}
	if cfg.TransitionTimeout == 0 {
		cfg.TransitionTimeout = defaultTransitionTimeout
	}

	return &pageHandler[S]{
		cfg:           cfg,
		clientHandler: newClientHandler(nil),
	}
}

// pageHandler serves stateless pages via plain HTTP request/response.
type pageHandler[S any] struct {
	cfg           PageConfig[S]
	clientHandler http.Handler
}

func (p *pageHandler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_poly/") {
		http.StripPrefix("/_poly", p.clientHandler).ServeHTTP(w, r)
		return
	}

	switch r.Method {
	case "GET":
		p.serveGET(w, r)
	case "POST":
		p.servePOST(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (p *pageHandler[S]) serveGET(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			p.cfg.Logger.Error("panic in page render", "panic", v)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	state := p.cfg.State(r)
	if p.cfg.HandleParams != nil {
		cs := &captureSession{id: "", fx: &effects{}}
		params := Params{Path: r.URL.Path, Query: r.URL.Query()}
		state = p.cfg.HandleParams(cs, state, params)
	}

	tree := p.cfg.Render(state)
	html := tree.Render()

	content := &polyBody{
		html:              html,
		endpoint:          r.URL.Path,
		transport:         mode.Fetch,
		defaultDebounce:   p.cfg.DefaultDebounce,
		transitionTimeout: p.cfg.TransitionTimeout,
		devMode:           p.cfg.DevMode,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "same-origin")
	if p.cfg.DevMode {
		w.Header().Set("Cache-Control", "no-store")
	}
	if p.cfg.Layout != nil {
		p.cfg.Layout(state, content).Render(w)
	} else {
		content.Render(w)
	}
}

func (p *pageHandler[S]) servePOST(w http.ResponseWriter, r *http.Request) {
	if !checkOrigin(r, p.cfg.AllowedOrigins) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			p.cfg.Logger.Error("panic in page handler", "panic", v)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, p.cfg.MaxEventBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}

	state := p.cfg.State(r)

	fx := &effects{}
	cs := &captureSession{id: "", fx: fx}
	if p.cfg.HandleParams != nil {
		params := Params{Path: r.URL.Path, Query: r.URL.Query()}
		state = p.cfg.HandleParams(cs, state, params)
	}

	state = p.cfg.Handle(cs, state, ev)

	tree := p.cfg.Render(state)
	html := tree.Render()

	u := update{
		Morphs:  []morph{{Key: "", HTML: html}},
		EventID: ev.EventID,
	}
	fx.merge(&u)

	data, err := marshalUpdate(u)
	if err != nil {
		p.cfg.Logger.Error("encode response error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
