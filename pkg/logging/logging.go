package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	levelVar = new(slog.LevelVar)
	logger   *slog.Logger
	once     sync.Once
)

func initLogger() {
	once.Do(func() {
		levelVar.Set(slog.LevelInfo)
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: levelVar,
		}))
	})
}

// Logger exposes the singleton slog logger used across the application.
func Logger() *slog.Logger {
	initLogger()
	return logger
}

// SetLevel configures the current logging level.
func SetLevel(level string) error {
	initLogger()

	switch strings.ToLower(level) {
	case "debug":
		levelVar.Set(slog.LevelDebug)
	case "info", "":
		levelVar.Set(slog.LevelInfo)
	case "warn", "warning":
		levelVar.Set(slog.LevelWarn)
	case "error":
		levelVar.Set(slog.LevelError)
	default:
		return fmt.Errorf("unknown log level %q", level)
	}
	return nil
}
