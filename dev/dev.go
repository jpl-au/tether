// Package dev provides tether's internal logging and development mode.
// All log output from the framework flows through this package so the
// logger is scoped to tether and never touches the process-wide slog
// default.
//
// [Warn] and [Debug] are gated behind dev mode - they help developers
// catch mistakes early but would be noise in production. For
// production observability, subscribe to [tether.Handler].Diagnostics.
//
// The package also provides [Log] for direct access to the scoped
// logger. The framework uses this for safety-net logging (panics,
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
