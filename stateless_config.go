package tether

import (
	"net/http"

	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/wire"
)

// StatelessConfig wires together a stateless page: how to reconstruct
// state from each request, how to render it, and how to handle events.
// Unlike [StatefulConfig], there is no persistent transport, no session
// pool, and no command loop - each request is independent. The server
// reconstructs state, renders HTML, and returns the response.
//
// State is reconstructed from each request - nothing persists between
// interactions. For pages with persistent connections and session
// state, use [StatefulConfig] with [Stateful] instead.
//
// GET requests render the full page. POST requests handle a client
// event, render the new state, and return an update with a root morph
// (or targeted fragments via [Session.Morph]) and any side effects -
// as a JSON envelope by default, or plain HTML with [wire.HTML].
//
// At minimum, set InitialState, Render, and Handle. Everything else
// is optional and has sensible defaults. Shared settings (DevMode,
// Logger, Client, Security, Assets) live on [App].
type StatelessConfig[S any] struct {
	// InitialState returns the starting state for each request. Called
	// on every request (GET and POST). Derive state from the URL,
	// cookies, headers, or a database - not from r.Body, which
	// contains the event JSON on POST requests.
	InitialState func(r *http.Request) S

	// Render builds a node tree from the current state. Same type as
	// [StatefulConfig].Render - a pure function that returns a Fluent node
	// tree. The same render function can be used for both live and
	// stateless pages.
	Render RenderFunc[S]

	// Handle processes a client event and returns the new state. Side
	// effects (toast, navigate, title, etc.) are expressed as calls on
	// the [Session] parameter - the same interface used by
	// [StatefulConfig].OnNavigate. The effects are included in the
	// response so the client can apply them atomically.
	Handle func(session Session, state S, event Event) S

	// Middleware wraps the Handle function with cross-cutting behaviour
	// such as logging, authentication, or metrics. Applied
	// outermost-first, matching [StatefulConfig].Middleware. Optional.
	Middleware []Middleware[S]

	// OnNavigate processes URL parameters on every request. Called
	// after State on both GET and POST. Same signature and redirect
	// behaviour as [StatefulConfig].OnNavigate - redirects via
	// [Session.Navigate] are resolved inline. Optional.
	OnNavigate func(session Session, state S, params Params) S

	// Layout wraps the page content in a full HTML document. Runs on
	// every GET request (stateless pages reconstruct state each time).
	// Signal bindings in the Layout shell work document-wide - see
	// [StatefulConfig].Layout for details. Optional.
	Layout func(state S, content node.Node) node.Node

	// Limits groups capacity constraints. Only MaxEventBytes is
	// relevant for stateless pages.
	Limits Limits

	// Components declares component mounts for automatic event
	// routing, matching [StatefulConfig].Components. Events whose action
	// matches a mount's prefix are dispatched to the component
	// before Handle runs. Optional.
	Components []ComponentMount[S]

	// Name identifies this page handler in log output. Appears in the
	// "tether: ready" startup line. Optional.
	Name string

	// WireFormat selects the encoding for POST event responses.
	// Defaults to [wire.JSON] (the same envelope stateful mode uses).
	// Set to [wire.HTML] for plain-HTML responses: the morph
	// fragments are the response body and side effects ride in a
	// small JSON island - curl-inspectable, no envelope overhead.
	// [wire.CBOR] is not supported in stateless mode.
	WireFormat wire.Format

	// AutoFragments enables automatic targeted updates by content
	// hash. The initial GET seeds the client with a hash per Dynamic
	// fragment; each event echoes the map, and the response carries
	// only the fragments whose content changed (plus the refreshed
	// map). No Session.Morph calls needed - though an explicit Morph
	// still takes precedence for that response. When the page's
	// fragment structure changes (Dynamic keys added or removed),
	// the response falls back to a full root morph.
	//
	// Requires Dynamic keys on the regions that change; a page with
	// no keys always sends full morphs, exactly as without this
	// flag.
	AutoFragments bool

	// CacheControl sets the Cache-Control header on GET responses.
	// Stateless pages embed no session token, so they are safe to
	// cache when the content permits it:
	//
	//	CacheControl: "public, max-age=60",
	//
	// Empty (the default) sends no Cache-Control header in
	// production; dev mode always sends no-store so edits show
	// immediately.
	CacheControl string
}
