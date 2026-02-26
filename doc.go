// Package poly is a reactive UI layer for Go. The server owns
// application state and renders HTML; the client owns ephemeral UI
// state — toggling drawers, binding text to signals, showing and hiding
// elements — without a round-trip. A persistent transport (WebSocket or
// SSE) keeps the two in sync: the server pushes targeted DOM patches and
// reactive signal updates, the client forwards user events back.
//
// The lifecycle of a page visit is:
//
//  1. The browser GETs the page. The handler renders the initial HTML
//     from [Config].InitialState and [Config].Render, pre-warms a
//     session with the diff state, and embeds the session ID in the
//     root element.
//  2. The client JS opens a persistent transport and reclaims the
//     pre-warmed session. Pre-warming avoids a second render on connect
//     and ensures the diff baseline matches the HTML the browser
//     already has.
//  3. When the user interacts with the page, the client sends an
//     [Event]. The server calls [Config].Handle to produce new state,
//     diffs the old and new render trees, and sends only the changed
//     fragments back as targeted patches or structural morphs.
//  4. For lightweight updates that don't need a full render cycle, the
//     server pushes signals via [Session.Signal]. Bound elements
//     ([bind.BindText], [bind.BindShow], [bind.BindClass],
//     [bind.BindAttr]) update instantly on the client — no diff, no
//     HTML.
//
// The central type is [Config], which wires together state, rendering,
// and event handling. Pass it to [New] to get an [http.Handler] that
// manages the full session lifecycle.
//
// Event binding helpers ([bind.Click], [bind.Submit], [bind.Input],
// etc.) attach data-poly-* attributes to Fluent elements so the client
// JS knows which DOM events to forward. Signal bindings and client-side
// directives handle interactions that stay in the browser. See the bind
// sub-package for the full set.
package poly
