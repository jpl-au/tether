// Package dev provides development-mode diagnostics. Call Enable
// once at startup; Warn, Debug, and Error silently no-op when dev
// mode is inactive. This centralises the dev-mode gate so call
// sites stay clean — dev.Warn("msg", "key", val) instead of
// scattered if-checks.
package dev

import "log/slog"

var mode bool

// Enable activates dev-mode diagnostics. Called once during handler
// initialisation when Config.DevMode is true or POLY_DEV is set.
func Enable() { mode = true }

// Enabled reports whether dev mode is active.
func Enabled() bool { return mode }

// Reset disables dev mode. Intended for tests that need to verify
// behaviour with dev mode off after another test has enabled it.
func Reset() { mode = false }

// Warn logs a warning only when dev mode is active. Use for
// diagnostics that help developers catch mistakes early (missing
// Dynamic keys, discarded effects, etc.) but would be noise in
// production.
func Warn(msg string, args ...any) {
	if mode {
		slog.Warn(msg, args...)
	}
}

// Debug logs a debug message only when dev mode is active.
func Debug(msg string, args ...any) {
	if mode {
		slog.Debug(msg, args...)
	}
}

// Error logs an error only when dev mode is active.
func Error(msg string, args ...any) {
	if mode {
		slog.Error(msg, args...)
	}
}
