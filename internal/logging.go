package internal

import (
	"log/slog"
	"os"
	"strings"
)

// ConfigureLogging mengatur default slog logger menjadi JSON handler sesuai
// konvensi project (structured log JSON) dengan level dari env LOG_LEVEL
// (default "info"). Panggil di awal tiap main() service.
//
// Level yang didukung: debug, info, warn, error.
func ConfigureLogging() {
	levelStr := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))

	var lvl slog.Level
	switch levelStr {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
		if levelStr != "" && levelStr != "info" {
			// Level tidak dikenal -> fallback info (jangan silent).
			slog.Warn("unknown LOG_LEVEL, falling back to info", "log_level", levelStr)
		}
	}

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
	slog.Info("logging configured", "level", lvl.String())
}
