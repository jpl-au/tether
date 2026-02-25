// Package poly is a reactive server-driven UI layer for Go. The server
// holds all application state and renders HTML; the browser is a thin
// display layer that forwards DOM events and morphs the page in place.
// This keeps business logic on the server while delivering the
// responsiveness of a single-page app.
//
// The lifecycle of a page visit is:
//
//  1. The browser GETs the page. The handler renders the initial HTML
//     from [Config].InitialState and [Config].Render, pre-warms a
//     session with the diff state, and embeds the session ID in the
//     root element.
//  2. The client JS opens a persistent transport (WebSocket or SSE)
//     and reclaims the pre-warmed session. Pre-warming avoids a second
//     render on connect and ensures the diff baseline matches the HTML
//     the browser already has.
//  3. When the user interacts with the page, the client sends an
//     [Event]. The server calls [Config].Handle to produce new state,
//     diffs the old and new render trees, and sends only the changed
//     fragments back as targeted patches or structural morphs.
//  4. The client applies patches by swapping innerHTML on keyed
//     elements, or applies morphs via idiomorph when the tree structure
//     changes. Idiomorph preserves input focus, scroll position, and
//     form state during morphs.
//
// The central type is [Config], which wires together state, rendering,
// and event handling. Pass it to [New] to get an [http.Handler] that
// manages the full session lifecycle.
//
// Event binding helpers ([bind.Click], [bind.Submit], [bind.Input],
// etc.) attach data-poly-* attributes to Fluent elements so the client
// JS knows which DOM events to forward. See the bind sub-package for
// the full set of helpers including client-side directives, loading
// states, and JS hooks.
package poly
