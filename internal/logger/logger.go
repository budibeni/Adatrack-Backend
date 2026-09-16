package logger

import (
	"log/slog"
	"os"
)

var Log *slog.Logger

func InitLogger() {
	opts := &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		AddSource:   true,
	}
	// Use JSON structured logging required for enterprise apps
	Log = slog.New(slog.NewJSONHandler(os.Stdout, opts))
	slog.SetDefault(Log)
}
