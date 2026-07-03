package tether

import jit "github.com/jpl-au/fluent-jit"

// SetSharedCacheSize configures the process-global shared-fragment
// cache used by [node.Shared] regions. n is the per-generation entry
// cap; the cache holds at most twice that many rendered fragments
// across its two generations. Call once at startup, before serving
// traffic.
//
// The cache is shared across every handler in the process, so this is a
// package-level setting, not a per-handler one. The default (2048)
// suits most applications; raise it only if you broadcast many distinct
// shared regions. Shared regions require [StatefulConfig].Memoise to be
// enabled - the differ engine does not consult the cache.
//
// This re-exports [jit.SetSharedCacheSize] so applications need not
// import fluent-jit directly.
func SetSharedCacheSize(n int) { jit.SetSharedCacheSize(n) }
