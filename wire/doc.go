// Package wire defines the encoding abstraction for server-to-client
// updates.
//
// The [Format] type selects which [Encoder] implementation serialises
// updates. Three formats are provided:
//
//   - [JSON] encodes as a single JSON object. This is the default.
//   - [CBOR] encodes as a binary CBOR map (RFC 8949) for smaller
//     payloads and faster encoding.
//   - [HTML] sends updates as plain HTML for the stateless,
//     one-request-one-response shape (no client runtime state).
//
// Set [Format] on [tether.App].WireFormat for an app-wide default, or
// on [tether.StatefulConfig].WireFormat for a specific handler.
// Per-handler settings override the app default. The zero value is
// [JSON].
//
// The set of formats is closed. [Encoder] is exported so callers can
// wrap or inspect an encoding, but tether resolves a [Format] to its
// encoder internally: a constant declared outside this package falls
// through to [JSON]. Adding a format means adding it here and to that
// resolution.
package wire
