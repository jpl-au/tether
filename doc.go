// Package tether is a reactive UI layer for Go. The server owns
// application state and renders HTML; the client owns ephemeral UI
// state - toggling drawers, binding text to signals, showing and hiding
// elements - without a round-trip.
//
// Tether provides two handler modes:
//
// [Stateful] handlers maintain a persistent connection (WebSocket or SSE)
// between browser and server. State survives across interactions  -
// when the user clicks a button, the server updates state and pushes
// the change without a page reload. Use Stateful for interactive
// applications: dashboards, forms, chat, real-time collaboration.
//
// [Stateless] handlers reconstruct state from each HTTP request. No
// persistent connection - every interaction is a standard
// request/response cycle. Use Stateless for content-focused pages that
// don't need real-time updates.
//
// Both modes share the same rendering engine (Fluent), the same event
// system, and the same component model. The difference is whether
// state persists between interactions.
//
// # Stateful mode
//
// The lifecycle of a stateful page visit is:
//
//  1. The browser GETs the page. The handler renders the initial HTML
//     from [StatefulConfig].InitialState and [StatefulConfig].Render, pre-warms
//     a session with the diff state, and embeds the session ID in the
//     root element.
//  2. The client JS opens a persistent transport and reclaims the
//     pre-warmed session. Pre-warming avoids a second render on connect
//     and ensures the diff baseline matches the HTML the browser
//     already has.
//  3. When the user interacts with the page, the client sends an
//     [Event]. The server calls [StatefulConfig].Handle to produce new
//     state, diffs the old and new render trees, and sends only the
//     changed fragments back as targeted patches or structural morphs.
//  4. For lightweight updates that don't need a full render cycle, the
//     server pushes signals via [Session.Signal]. Bound elements
//     ([bind.Text], [bind.Show], [bind.Class],
//     [bind.Attr]) update instantly on the client - no diff, no
//     HTML.
//
// # Stateless mode
//
// GET requests render the full HTML page. POST requests handle a
// client event, render the new state, and return a JSON update with
// the new HTML and any side effects. The client applies the update
// without a full page reload.
//
// Event binding helpers ([bind.OnClick], [bind.OnSubmit], [bind.OnInput],
// etc.) attach data-tether-* attributes to Fluent elements so the client
// JS knows which DOM events to forward. Signal bindings and client-side
// directives handle interactions that stay in the browser. See the bind
// sub-package for the full set.
//
// # Wire format
//
// Server-to-client updates are encoded as JSON by default. Set
// [App].WireFormat or [StatefulConfig].WireFormat to [wire.CBOR] for
// smaller, faster binary payloads. The client detects the format
// automatically - no additional configuration is needed on the browser
// side.
//
// # Client runtime
//
// The default client runtime is tether.js, a JavaScript implementation
// that handles transport, morphing, signals, and event binding. For
// applications that benefit from shared Go types on both sides of the
// wire, set [App].Client.Runtime to [Runtime.WASM] to use a Go WASM
// client instead. See the tether-wasm module for details.
package tether
