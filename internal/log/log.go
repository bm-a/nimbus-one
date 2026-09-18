// Package log provides a structured slog singleton.
package log

import (
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	mu     sync.Mutex
	logger *slog.Logger
)

// Init configures the global logger. level: debug|info|warn|error.
func Init(level string) *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	logger = slog.New(h)
	slog.SetDefault(logger)
	return logger
}

// L returns the global logger, initializing with info level if needed.
func L() *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	if logger == nil {
		h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
		logger = slog.New(h)
		slog.SetDefault(logger)
	}
	return logger
}
