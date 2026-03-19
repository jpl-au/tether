package tether

import (
	"log/slog"
	"os"
	"sync"

	"github.com/jpl-au/tether/dev"
)

// loggerOnce ensures the process-wide slog default is set only once.
var loggerOnce sync.Once

// setDefaultLogger sets the process-wide slog default, but only on
// the first call. Safe to call from multiple goroutines.
func setDefaultLogger(l *slog.Logger) {
	loggerOnce.Do(func() { slog.SetDefault(l) })
}

// setupLogging resolves DevMode from the environment, creates a
// default logger when none is provided, sets the process-wide slog
// default (once), and enables the dev package. Called by both
// [Live] and [Page] so the logging setup is identical.
func setupLogging(a *App) {
	if !a.DevMode && os.Getenv("TETHER_DEV") != "" {
		a.DevMode = true
	}
	if a.Logger == nil {
		level := slog.LevelInfo
		if a.DevMode {
			level = slog.LevelDebug
		}
		opts := &slog.HandlerOptions{Level: level}
		a.Logger = slog.New(slog.NewTextHandler(os.Stderr, opts))
		setDefaultLogger(a.Logger)
	}
	if a.DevMode {
		dev.Enable()
	}
}
