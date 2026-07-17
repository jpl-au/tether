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

	if app.Cluster != nil {
		SetCluster(app.Cluster)
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
	if cfg.Limits.MaxNavigateRedirects == 0 {
		cfg.Limits.MaxNavigateRedirects = defaultMaxNavigateRedirects
	}
	app.Client.defaults()

	csrf := app.Security.csrf()

	// Resolve the wire format: per-handler config takes precedence,
	// then the app-level default. Stateless supports JSON (the
	// default envelope, shared with stateful mode) and HTML (plain
	// fragments). CBOR is stateful-only - the fetch client decodes
	// text - so it falls back to JSON with a warning rather than
	// being silently honoured.
	wf := cfg.WireFormat
	if wf == 0 && app.WireFormat != 0 {
		wf = app.WireFormat
	}
	if wf == wire.CBOR {
		dev.Log().Warn("tether: Stateless does not support wire.CBOR - using JSON; CBOR applies to stateful handlers only")
		wf = wire.JSON
	}

	return &statelessHandler[S]{
		app:           app,
		cfg:           cfg,
		wireFormat:    wf,
		encoder:       resolveEncoder(wire.JSON),
		clientHandler: app.jsHandler(),
		assetMounts:   app.mountAssets(),
		csrf:          csrf,
	}
}

// statelessHandler serves stateless pages via plain HTTP request/response.
type statelessHandler[S any] struct {
	app           App
	cfg           StatelessConfig[S]
	wireFormat    wire.Format
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
		// A Navigate call during a GET is an auth-guard style
		// redirect - answer with a real HTTP redirect instead of
		// rendering the guarded page.
		if cs.Effects.URL != "" {
			http.Redirect(w, r, cs.Effects.URL, http.StatusFound)
			return
		}
	}

	tree := p.cfg.Render(state)

	// Seed the client's fragment-hash map. The page renders once;
	// fragments are byte ranges of that render, so the seeded hashes
	// always describe the exact bytes the client receives. The island
	// rides inside the tether root so the runtime finds it on init;
	// standard JSON escaping keeps "</template>" out of the payload.
	var html []byte
	if p.cfg.AutoFragments {
		var exts []extent
		html, exts = renderPage(tree)
		frags := fragments(html, exts)

		if dev.Enabled() {
			warnUnstableFragments(tree, frags, r.URL.Path)
		}

		if hashes := fragmentHashes(frags); len(hashes) > 0 {
			if data, err := json.Marshal(hashes); err == nil {
				html = append(html, []byte(`<template data-tether-hashes>`)...)
				html = append(html, data...)
				html = append(html, []byte(`</template>`)...)
			}
		}
	} else {
		html = tree.RenderBytes()
	}

	content := &tetherBody{
		html:              html,
		endpoint:          r.URL.Path,
		transport:         mode.HTTP,
		defaultDebounce:   p.app.Client.DefaultDebounce,
		transitionTimeout: p.app.Client.TransitionTimeout,
		flashDuration:     p.app.Client.FlashDuration,
		toastDuration:     p.app.Client.ToastDuration,
		viewTransitions:   p.app.Client.ViewTransitions,
		runtime:           p.app.Client.Runtime,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	// Stateless pages embed no session token, so caching is the
	// developer's call via CacheControl. Dev mode always disables
	// caching so edits show immediately.
	switch {
	case dev.Enabled():
		w.Header().Set("Cache-Control", "no-store")
	case p.cfg.CacheControl != "":
		w.Header().Set("Cache-Control", p.cfg.CacheControl)
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
		for i := range p.cfg.Limits.MaxNavigateRedirects {
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
			if i == p.cfg.Limits.MaxNavigateRedirects-1 {
				dev.Log().Warn("navigate redirect limit reached",
					"url", cs.Effects.URL)
				cs.Effects.Replace = true
			}
		}
	}

	// One render serves the whole response: morphs, hashes and the
	// full-page fallback are all byte ranges of the same pass, so the
	// client can never store a hash for content it was not sent.
	tree := p.cfg.Render(state)
	html, exts := renderPage(tree)

	var u wire.Update
	switch {
	case len(cs.MorphKeys) > 0:
		// Explicit Morph always wins for this response.
		morphs := morphsFor(html, exts, cs.MorphKeys)
		if dev.Enabled() {
			found := make(map[string]bool, len(morphs))
			for _, m := range morphs {
				found[m.Key] = true
			}
			for _, key := range cs.MorphKeys {
				if !found[key] {
					dev.Warn("Morph key not found in rendered tree",
						"key", key, "path", r.URL.Path)
				}
			}
		}
		u = wire.Update{
			Morphs:  morphs,
			EventID: ev.EventID,
		}

	case p.cfg.AutoFragments && ev.Hashes != nil:
		u = autoFragmentUpdate(html, exts, ev)

	default:
		u = wire.Update{
			Morphs:  []wire.Morph{{Key: "", HTML: html}},
			EventID: ev.EventID,
		}
	}

	// With auto-fragments on, every response carries the complete
	// fresh hash map so the client can echo it with the next event -
	// including on the explicit-Morph and fallback paths, which would
	// otherwise leave the client's map stale.
	if p.cfg.AutoFragments && u.Hashes == nil {
		u.Hashes = fragmentHashes(fragments(html, exts))
	}
	cs.Effects.merge(&u)

	if p.wireFormat == wire.HTML {
		body, keyed, err := wire.HTMLBody(u)
		if err != nil {
			dev.Log().Error("encode response error", "err", err, "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Keyed fragments need an explicit signal: a root render whose
		// top-level elements all carry keys would otherwise look
		// identical to a fragment response on the client.
		if keyed {
			w.Header().Set("Tether-Morph", "keyed")
		}
		if _, err := w.Write(body); err != nil {
			dev.Log().Warn("failed to write page response", "path", r.URL.Path, "err", err)
		}
		return
	}

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

// autoFragmentUpdate builds a targeted update by comparing the fresh
// render's fragment hashes against the map the client echoed. Only
// changed fragments travel; the complete refreshed map rides along so
// the client stays current. Structural changes (Dynamic keys added or
// removed) and pages without keys fall back to a full root morph -
// the same bytes the fragments were sliced from, so the hashes sent
// with the fallback always describe the page the client now holds.
func autoFragmentUpdate(html []byte, exts []extent, ev Event) wire.Update {
	frags := fragments(html, exts)
	fresh := fragmentHashes(frags)

	if len(frags) == 0 || !sameKeys(ev.Hashes, fresh) {
		return wire.Update{
			Morphs:  []wire.Morph{{Key: "", HTML: html}},
			EventID: ev.EventID,
			Hashes:  fresh,
		}
	}

	var morphs []wire.Morph
	for key, hash := range fresh {
		if ev.Hashes[key] != hash {
			morphs = append(morphs, wire.Morph{Key: key, HTML: frags[key]})
		}
	}
	// morphs may be empty - nothing changed - and that is a valid
	// update: the event ID still echoes so loading state clears.
	return wire.Update{
		Morphs:  morphs,
		EventID: ev.EventID,
		Hashes:  fresh,
	}
}
