// Package wire defines the encoding abstraction for server-to-client
// updates.
//
// The [Format] type selects which [Encoder] implementation serialises
// updates. Two formats are provided:
//
//   - [JSON] encodes as a single JSON object. This is the default.
//   - [CBOR] encodes as a binary CBOR map (RFC 8949) for smaller
//     payloads and faster encoding.
//
// Set [Format] on [tether.App].WireFormat for an app-wide default, or
// on [tether.StatefulConfig].WireFormat for a specific handler.
// Per-handler settings override the app default. The zero value is
// [JSON].
//
// Custom encoders can implement the [Encoder] interface and be wired
// in via a new [Format] constant.
package wire
