package tether

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/wire"
)

// Page creates an [http.Handler] for a stateless page. State is
// reconstructed from each HTTP request — nothing persists between
// interactions. GET requests render the full HTML page. POST requests
// handle a client event and return a JSON update with the new HTML
// and any side effects.
//
// For live pages with persistent connections and session state, use
// [Live] instead.
func Page[S any](cfg PageConfig[S]) http.Handler {
	if cfg.State == nil {
		panic("tether: PageConfig.State is required")
	}
	if cfg.Render == nil {
		panic("tether: PageConfig.Render is required")
	}
	if cfg.Handle == nil {
		panic("tether: PageConfig.Handle is required")
	}

	if !cfg.DevMode && os.Getenv("TETHER_DEV") != "" {
		cfg.DevMode = true
	}
	if cfg.Logger == nil {
		level := slog.LevelInfo
		if cfg.DevMode {
			level = slog.LevelDebug
		}
		opts := &slog.HandlerOptions{Level: level}
		if cfg.LogJSON {
			cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, opts))
		} else {
			cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, opts))
		}
	}
	slog.SetDefault(cfg.Logger)
	if cfg.DevMode {
		dev.Enable()
	}

	if len(cfg.Middleware) > 0 {
		cfg.Handle = Chain(cfg.Handle, cfg.Middleware)
	}

	pageArgs := []any{"transport", "http"}
	if cfg.Name != "" {
		pageArgs = append(pageArgs, "name", cfg.Name)
	}
	if len(cfg.Middleware) > 0 {
		pageArgs = append(pageArgs, "middleware", middlewareNames(cfg.Middleware))
	}
	if cfg.DevMode {
		pageArgs = append(pageArgs, "dev", true)
	}
	cfg.Logger.Info("tether: ready", pageArgs...)

	if cfg.Limits.MaxEventBytes == 0 {
		cfg.Limits.MaxEventBytes = defaultMaxEventBytes
	}
	if cfg.Client.DefaultDebounce == 0 {
		cfg.Client.DefaultDebounce = defaultDefaultDebounce
	}
	if cfg.Client.TransitionTimeout == 0 {
		cfg.Client.TransitionTimeout = defaultTransitionTimeout
	}

	csrf := http.NewCrossOriginProtection()
	for _, origin := range cfg.Security.TrustedOrigins {
		if err := csrf.AddTrustedOrigin(origin); err != nil {
			panic("tether: invalid TrustedOrigins entry " + origin + ": " + err.Error())
		}
	}

	return &pageHandler[S]{
		cfg:           cfg,
		encoder:       wire.JSONEncoder{},
		clientHandler: newClientHandler(cfg.Assets),
		assetMounts:   buildAssetMounts(cfg.Assets),
		csrf:          csrf,
	}
}

// pageHandler serves stateless pages via plain HTTP request/response.
type pageHandler[S any] struct {
	cfg           PageConfig[S]
	encoder       wire.Encoder
	clientHandler http.Handler
	assetMounts   []assetMount
	csrf          *http.CrossOriginProtection
}

func (p *pageHandler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_tether/") {
		http.StripPrefix("/_tether", p.clientHandler).ServeHTTP(w, r)
		return
	}

	for _, m := range p.assetMounts {
		if strings.HasPrefix(r.URL.Path, m.prefix) {
			m.handler.ServeHTTP(w, r)
			return
		}
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
			slog.Error("panic in page render", "panic", v, "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	state := p.cfg.State(r)
	if p.cfg.OnNavigate != nil {
		cs := &CaptureSession{PushErr: ErrPushPreWarm}
		params := Params{Path: r.URL.Path, Query: r.URL.Query()}
		state = p.cfg.OnNavigate(cs, state, params)
	}

	tree := p.cfg.Render(state)
	html := tree.Render()

	content := &tetherBody{
		html:              html,
		endpoint:          r.URL.Path,
		transport:         mode.HTTP,
		defaultDebounce:   p.cfg.Client.DefaultDebounce,
		transitionTimeout: p.cfg.Client.TransitionTimeout,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	if dev.Enabled() {
		w.Header().Set("Cache-Control", "no-store")
	}
	if p.cfg.Layout != nil {
		p.cfg.Layout(state, content).Render(w)
	} else {
		content.Render(w)
	}
}

func (p *pageHandler[S]) servePOST(w http.ResponseWriter, r *http.Request) {
	if err := p.csrf.Check(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			slog.Error("panic in page handler", "panic", v, "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, p.cfg.Limits.MaxEventBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "malformed JSON event", http.StatusBadRequest)
		return
	}

	state := p.cfg.State(r)

	cs := &CaptureSession{PushErr: ErrPushPreWarm}
	if ev.Type == event.Navigate && p.cfg.OnNavigate != nil {
		// Navigate events carry the target path in event data, not
		// in the request URL (the client always POSTs to its
		// endpoint). Read path and search from ev.Data to match
		// the live handler in handler.go.
		params := Params{Path: ev.Data["path"]}
		if search := ev.Data["search"]; search != "" {
			var err error
			params.Query, err = url.ParseQuery(strings.TrimPrefix(search, "?"))
			if err != nil {
				slog.Warn("malformed query string in navigate event", "search", search, "err", err)
			}
		}
		state = p.cfg.OnNavigate(cs, state, params)
	} else {
		// For all other events, derive state from the URL first
		// (stateless mode reconstructs state each request), then
		// process the event via Handle.
		if p.cfg.OnNavigate != nil {
			params := Params{Path: r.URL.Path, Query: r.URL.Query()}
			state = p.cfg.OnNavigate(cs, state, params)
		}
		if newState, ok := RouteMount(p.cfg.Components, cs, state, ev); ok {
			state = newState
		} else {
			state = p.cfg.Handle(cs, state, ev)
		}
	}

	tree := p.cfg.Render(state)
	html := tree.Render()

	u := wire.Update{
		Morphs:  []wire.Morph{{Key: "", HTML: html}},
		EventID: ev.EventID,
	}
	cs.Effects.merge(&u)

	data, err := p.encoder.Encode(u)
	if err != nil {
		slog.Error("encode response error", "err", err, "path", r.URL.Path, "remote", r.RemoteAddr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		slog.Warn("failed to write page response", "path", r.URL.Path, "err", err)
	}
}
