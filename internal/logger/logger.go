package logger

import (
	"log/slog"
	"os"
)

var Log *slog.Logger

func InitLogger() {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}
	Log = slog.New(slog.NewJSONHandler(os.Stdout, opts))
	slog.SetDefault(Log)
}
