package logger
import (
	"log/slog"
	"os"
)
var Log *slog.Logger = slog.Default()

func InitLogger() {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo, AddSource: true}
	Log = slog.New(slog.NewJSONHandler(os.Stdout, opts))
	slog.SetDefault(Log)
}
