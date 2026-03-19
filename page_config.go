package tether

import (
	"net/http"

	"github.com/jpl-au/fluent/node"
)

// PageConfig wires together a stateless page: how to reconstruct state
// from each request, how to render it, and how to handle events. Unlike
// [LiveConfig], there is no persistent transport, no session pool, and
// no command loop — each request is independent. The server
// reconstructs state, renders HTML, and returns the response.
//
// A page handler uses traditional HTTP request/response. State is
// reconstructed from each request — nothing persists between
// interactions. For live pages with persistent connections and session
// state, use [LiveConfig] with [Live] instead.
//
// GET requests render the full page. POST requests handle a client
// event, render the new state, and return a JSON update (the same wire
// format as live mode) with a root morph and any side effects.
//
// At minimum, set InitialState, Render, and Handle. Everything else
// is optional and has sensible defaults. Shared settings (DevMode,
// Logger, Client, Security, Assets) live on [App].
type PageConfig[S any] struct {
	// InitialState returns the starting state for each request. Called
	// on every request (GET and POST). Derive state from the URL,
	// cookies, headers, or a database — not from r.Body, which
	// contains the event JSON on POST requests.
	InitialState func(r *http.Request) S

	// Render builds a node tree from the current state. Same type as
	// [LiveConfig].Render — a pure function that returns a Fluent node
	// tree. The same render function can be used for both live and
	// stateless pages.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state. Side
	// effects (toast, navigate, title, etc.) are expressed as calls on
	// the [Session] parameter — the same interface used by
	// [LiveConfig].OnNavigate. The effects are included in the JSON
	// response so the client can apply them atomically.
	Handle func(session Session, state S, event Event) S

	// Middleware wraps the Handle function with cross-cutting behaviour
	// such as logging, authentication, or metrics. Applied
	// outermost-first, matching [LiveConfig].Middleware. Optional.
	Middleware []Middleware[S]

	// OnNavigate processes URL parameters on every request. Called
	// after State on both GET and POST. Same signature as
	// [LiveConfig].OnNavigate. Optional.
	OnNavigate func(session Session, state S, params Params) S

	// Layout wraps the page content in a full HTML document. Runs on
	// every GET request (stateless pages reconstruct state each time).
	// Signal bindings in the Layout shell work document-wide — see
	// [LiveConfig].Layout for details. Optional.
	Layout func(state S, content node.Node) node.Node

	// Limits groups capacity constraints. Only MaxEventBytes is
	// relevant for stateless pages.
	Limits Limits

	// Components declares component mounts for automatic event
	// routing, matching [LiveConfig].Components. Events whose action
	// matches a mount's prefix are dispatched to the component
	// before Handle runs. Optional.
	Components []ComponentMount[S]

	// Name identifies this page handler in log output. Appears in the
	// "tether: ready" startup line. Optional.
	Name string
}
