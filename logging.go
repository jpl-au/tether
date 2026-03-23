package tether

import (
	"log/slog"
	"os"

	"github.com/jpl-au/tether/dev"
)

// initLog resolves DevMode from the environment, creates a default
// logger when none is provided, configures the dev package's scoped
// logger, and enables dev mode. The process-wide slog default is
// never modified - tether's logger is scoped to the framework.
func (a *App) initLog() {
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
	}
	dev.SetLogger(a.Logger)
	if a.DevMode {
		dev.Enable()
	}
}
