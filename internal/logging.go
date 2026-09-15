package internal

import (
	"log/slog"
	"os"
	"strings"
)

// ConfigureLogging installs the process-wide structured JSON logger (PRD §7/§10):
// level comes from LOG_LEVEL (debug|info|warn|error, default info) and the output
// is JSON on stdout so container logs stay machine-parseable. Never use
// fmt.Println for logging (.agent/01-global-rules.md §7).
func ConfigureLogging() {
	level := parseLevel(os.Getenv("LOG_LEVEL"))
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Keep timestamps in RFC3339 with milliseconds (log correlation).
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z07:00"))
			}
			return a
		},
	})
	logger := slog.New(handler).With("service", serviceName())
	slog.SetDefault(logger)
}

// parseLevel maps LOG_LEVEL to a slog level, defaulting to info.
func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// serviceName identifies the running binary for log/audit correlation.
func serviceName() string {
	if name := os.Getenv("SERVICE_NAME"); name != "" {
		return name
	}
	if len(os.Args) > 0 && os.Args[0] != "" {
		return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(os.Args[0], "./"), "bin/"), ".exe")
	}
	return "unknown"
}
