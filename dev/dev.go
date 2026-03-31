// Package dev provides tether's internal logging and development mode.
// It is a debugging aid for framework developers, not a production
// observability API. For production monitoring, alerting, and metrics,
// subscribe to [tether.Handler].Diagnostics which provides typed,
// per-handler events via [tether.Bus].
//
// # Design: two-tier observability
//
// Tether separates observability into two tiers:
//
//   - dev (this package): verbose, human-readable log output gated
//     behind DevMode. Process-wide and intentionally global - dev mode
//     is a property of the development session, not of individual
//     handlers. When enabled, every handler logs to the same scoped
//     logger. This is by design: during development you want to see
//     the full picture, not per-handler silos.
//
//   - Diagnostics ([tether.Bus]): typed, per-handler events for
//     production use. Subscribe to observe panics, transport errors,
//     buffer overflows, and render statistics. Route them to your
//     metrics pipeline, alerting system, or structured logger.
//
// The logger and enabled flag are package-level globals. This is safe
// because dev mode is set once at startup (from [tether.App].DevMode
// or the TETHER_DEV environment variable) and applies uniformly to all
// handlers in the process. Multiple App instances sharing the same dev
// state is the intended behaviour, not a bug.
//
// [Warn] and [Debug] are gated behind dev mode - they help developers
// catch mistakes early but would be noise in production. [Log] gives
// direct access to the scoped logger for safety-net logging (panics,
// critical errors) that must always fire regardless of dev mode.
//
// Call [SetLogger] at startup to configure the logger; when not set,
// the process-wide slog default is used as a fallback.
package dev

import (
	"log/slog"
	"sync/atomic"
)

var (
	enabled atomic.Bool
	logger  atomic.Pointer[slog.Logger]
)

// SetLogger configures the logger for all tether log output. When
// not called, the process-wide slog default is used as a fallback.
// Called once during handler initialisation.
func SetLogger(l *slog.Logger) {
	logger.Store(l)
}

// Enable activates dev-mode diagnostics. Called once during handler
// initialisation when App.DevMode is true or TETHER_DEV is set.
func Enable() { enabled.Store(true) }

// Enabled reports whether dev mode is active.
func Enabled() bool { return enabled.Load() }

// Reset disables dev mode and clears the logger. Intended for tests
// that need to verify behaviour with dev mode off after another test
// has enabled it.
func Reset() {
	enabled.Store(false)
	logger.Store(nil)
}

// Log returns tether's scoped logger. Use this for safety-net
// logging (panics, critical errors) that must always fire regardless
// of dev mode. For dev-only output, use [Warn] or [Debug] instead.
func Log() *slog.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// Warn logs a warning only when dev mode is active. Use for
// diagnostics that help developers catch mistakes early (missing
// Dynamic keys, discarded effects, etc.) but would be noise in
// production. For production observability, subscribe to
// Handler.Diagnostics.
func Warn(msg string, args ...any) {
	if enabled.Load() {
		Log().Warn(msg, args...)
	}
}

// Debug logs a debug message only when dev mode is active.
func Debug(msg string, args ...any) {
	if enabled.Load() {
		Log().Debug(msg, args...)
	}
}
