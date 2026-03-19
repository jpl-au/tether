package tether

import (
	"log/slog"
	"os"
	"sync"

	"github.com/jpl-au/tether/dev"
)

var once sync.Once

// initLog resolves DevMode from the environment, creates a default
// logger when none is provided, sets the process-wide slog default
// (once), and enables the dev package.
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
		once.Do(func() { slog.SetDefault(a.Logger) })
	}
	if a.DevMode {
		dev.Enable()
	}
}
