package tether

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/mode"
	"github.com/jpl-au/tether/wire"
)

// Page creates an [http.Handler] for a stateless page. State is
// reconstructed from each HTTP request - nothing persists between
// interactions. GET requests render the full HTML page. POST requests
// handle a client event and return a JSON update with the new HTML
// and any side effects.
//
// For stateful pages with persistent connections and session state, use
// [Stateful] instead.
func Stateless[S any](app App, cfg StatelessConfig[S]) http.Handler {
	if cfg.InitialState == nil {
		panic("tether: StatelessConfig.InitialState is required")
	}
	if cfg.Render == nil {
		panic("tether: StatelessConfig.Render is required")
	}
	if cfg.Handle == nil {
		panic("tether: StatelessConfig.Handle is required")
	}

	app.initLog()

	// Compose component routing into Handle so that mounted component
	// events flow through middleware, matching the Stateful composition.
	if len(cfg.Components) > 0 {
		appHandle := cfg.Handle
		components := cfg.Components
		cfg.Handle = func(sess Session, s S, ev Event) S {
			if ev.Type != event.Navigate {
				if newState, ok := RouteMount(components, sess, s, ev); ok {
					return newState
				}
			}
			return appHandle(sess, s, ev)
		}
	}

	// Compose OnNavigate into Handle so middleware covers navigate
	// events, matching the Stateful composition.
	if cfg.OnNavigate != nil {
		appHandle := cfg.Handle
		appNav := cfg.OnNavigate
		cfg.Handle = func(sess Session, s S, ev Event) S {
			if ev.Type == event.Navigate {
				return appNav(sess, s, paramsFromEvent(ev))
			}
			return appHandle(sess, s, ev)
		}
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
	if app.DevMode {
		pageArgs = append(pageArgs, "dev", true)
	}
	dev.Log().Info("tether: ready", pageArgs...)

	if cfg.Limits.MaxEventBytes == 0 {
		cfg.Limits.MaxEventBytes = defaultMaxEventBytes
	}
	app.Client.defaults()

	csrf := app.Security.csrf()

	return &statelessHandler[S]{
		app:           app,
		cfg:           cfg,
		encoder:       resolveEncoder(0),
		clientHandler: app.jsHandler(),
		assetMounts:   app.mountAssets(),
		csrf:          csrf,
	}
}

// statelessHandler serves stateless pages via plain HTTP request/response.
type statelessHandler[S any] struct {
	app           App
	cfg           StatelessConfig[S]
	encoder       wire.Encoder
	clientHandler http.Handler
	assetMounts   []assetMount
	csrf          *http.CrossOriginProtection
}

func (p *statelessHandler[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

func (p *statelessHandler[S]) serveGET(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			dev.Log().Error("panic in page render", "panic", v, "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	state := p.cfg.InitialState(r)
	if p.cfg.OnNavigate != nil {
		cs := &CaptureSession{Ctx: r.Context(), PushErr: ErrPushPreWarm}
		params := Params{Path: r.URL.Path, Query: r.URL.Query()}
		state = p.cfg.OnNavigate(cs, state, params)
	}

	tree := p.cfg.Render(state)
	html := tree.Render()

	content := &tetherBody{
		html:              html,
		endpoint:          r.URL.Path,
		transport:         mode.HTTP,
		defaultDebounce:   p.app.Client.DefaultDebounce,
		transitionTimeout: p.app.Client.TransitionTimeout,
		flashDuration:     p.app.Client.FlashDuration,
		toastDuration:     p.app.Client.ToastDuration,
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

func (p *statelessHandler[S]) servePOST(w http.ResponseWriter, r *http.Request) {
	if err := p.csrf.Check(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			dev.Log().Error("panic in page handler", "panic", v, "path", r.URL.Path, "remote", r.RemoteAddr)
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

	state := p.cfg.InitialState(r)
	cs := &CaptureSession{PushErr: ErrPushPreWarm}

	// In stateless mode, non-navigate events need URL-derived state as
	// a starting point because stateless mode reconstructs state from
	// each request. This is state preparation, not event dispatch.
	if ev.Type != event.Navigate && p.cfg.OnNavigate != nil {
		params := Params{Path: r.URL.Path, Query: r.URL.Query()}
		state = p.cfg.OnNavigate(cs, state, params)
	}

	// All events flow through the unified dispatch: middleware,
	// OnNavigate, component routing, then Handle.
	state = p.cfg.Handle(cs, state, ev)

	// Resolve navigate redirects inline, matching the stateful
	// behaviour in exec(). See loop.go for the full explanation.
	if ev.Type == event.Navigate && cs.Effects.URL != "" {
		for i := range maxNavigateRedirects {
			redirectURL := cs.Effects.URL
			u, err := url.Parse(redirectURL)
			if err != nil {
				dev.Log().Warn("malformed navigate redirect URL",
					"url", redirectURL, "error", err)
				break
			}

			cs.Effects.URL = ""
			cs.Effects.Replace = false
			redirectEv := Event{
				Type: event.Navigate,
				Data: map[string]string{"path": u.Path, "search": u.RawQuery},
			}
			state = p.cfg.Handle(cs, state, redirectEv)

			if cs.Effects.URL == "" {
				cs.Effects.URL = redirectURL
				cs.Effects.Replace = true
				break
			}
			if i == maxNavigateRedirects-1 {
				dev.Log().Warn("navigate redirect limit reached",
					"url", cs.Effects.URL)
				cs.Effects.Replace = true
			}
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
		dev.Log().Error("encode response error", "err", err, "path", r.URL.Path, "remote", r.RemoteAddr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		dev.Log().Warn("failed to write page response", "path", r.URL.Path, "err", err)
	}
}
