package dev

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSetLoggerRoutesToConfiguredLogger(t *testing.T) {
	defer Reset()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	SetLogger(logger)
	Enable()

	Warn("test warning", "key", "val")
	Debug("test debug", "key", "val")

	output := buf.String()
	if !strings.Contains(output, "test warning") {
		t.Error("Warn output not routed to configured logger")
	}
	if !strings.Contains(output, "test debug") {
		t.Error("Debug output not routed to configured logger")
	}
}

func TestLogFallsBackToSlogDefault(t *testing.T) {
	defer Reset()

	// No SetLogger called - Log() should return slog.Default().
	got := Log()
	want := slog.Default()
	if got != want {
		t.Error("Log() should return slog.Default() when no logger is set")
	}
}

func TestWarnAndDebugGatedBehindDevMode(t *testing.T) {
	defer Reset()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	SetLogger(logger)
	// Dev mode NOT enabled.

	Warn("should not appear")
	Debug("should not appear")

	if buf.Len() > 0 {
		t.Errorf("expected no output with dev mode off, got: %s", buf.String())
	}
}

func TestLogAlwaysFiresRegardlessOfDevMode(t *testing.T) {
	defer Reset()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	SetLogger(logger)
	// Dev mode NOT enabled.

	Log().Error("safety net error")

	if !strings.Contains(buf.String(), "safety net error") {
		t.Error("Log().Error should fire regardless of dev mode")
	}
}

func TestResetClearsLoggerAndMode(t *testing.T) {
	SetLogger(slog.Default())
	Enable()

	Reset()

	if Enabled() {
		t.Error("Enabled() should be false after Reset()")
	}
	// After reset, Log() falls back to slog.Default().
	if Log() != slog.Default() {
		t.Error("Log() should fall back to slog.Default() after Reset()")
	}
}
