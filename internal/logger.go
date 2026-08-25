package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// loggerField represents a key-value pair in log context.
type loggerField struct {
	Key   string
	Value interface{}
}

// Fields is a collection of log key-value pairs.
type Fields map[string]interface{}

// NewFields creates a new empty Fields map.
func NewFields() Fields {
	return make(Fields)
}

// Add adds a key-value pair to the Fields.
func (f Fields) Add(key string, value interface{}) Fields {
	f[key] = value
	return f
}

// String returns the log entry as a formatted string.
// This is a simple string formatter; for JSON output use ToJSON().
func (f Fields) String() string {
	if len(f) == 0 {
		return ""
	}
	var sb strings.Builder
	for k, v := range f {
		sb.WriteString(fmt.Sprintf("%s=%v ", k, v))
	}
	return strings.TrimSpace(sb.String())
}

// ToJSON returns the log entry as a JSON string.
func (f Fields) ToJSON() string {
	jsonData, err := json.Marshal(f)
	if err != nil {
		return "{}"
	}
	return string(jsonData)
}

// Logger is the interface for structured logging.
type Logger interface {
	// Debug logs a debug message.
	Debug(msg string, fields ...Fields)
	// Info logs an info message.
	Info(msg string, fields ...Fields)
	// Warn logs a warning message.
	Warn(msg string, fields ...Fields)
	// Error logs an error message.
	Error(msg string, fields ...Fields)
}

// stdoutLogger implements Logger using os.Stdout.
type stdoutLogger struct {
	level  logLevel
	prefix string
}

type logLevel int

const (
	logDebug logLevel = iota
	logInfo
	logWarn
	logError
)

// NewStdoutLogger creates a new Logger that outputs to os.Stdout.
// level can be: "debug", "info", "warn", "error"
func NewStdoutLogger(level string) Logger {
	var l logLevel
	switch strings.ToLower(level) {
	case "debug":
		l = logDebug
	case "info":
		l = logInfo
	case "warn":
		l = logWarn
	case "error":
		l = logError
	default:
		l = logInfo
	}
	return &stdoutLogger{level: l, prefix: ""}
}

// logWithFields formats and writes a log message with fields.
func (l *stdoutLogger) log(msg string, level logLevel, fields ...Fields) {
	var fs Fields
	if len(fields) > 0 && len(fields[0]) > 0 {
		fs = fields[0]
	}

	timestamp := time.Now().Format("2006-01-02T15:04:05Z07:00")
	levelStr := "DEBUG"
	switch level {
	case logInfo:
		levelStr = "INFO"
	case logWarn:
		levelStr = "WARN"
	case logError:
		levelStr = "ERROR"
	}

	output := fmt.Sprintf("%s [%s] %s %s\n", timestamp, levelStr, msg, fs.ToJSON())
	fmt.Fprint(os.Stdout, output)
}

// Debug logs a debug message.
func (l *stdoutLogger) Debug(msg string, fields ...Fields) {
	l.log(msg, logDebug, fields...)
}

// Info logs an info message.
func (l *stdoutLogger) Info(msg string, fields ...Fields) {
	l.log(msg, logInfo, fields...)
}

// Warn logs a warning message.
func (l *stdoutLogger) Warn(msg string, fields ...Fields) {
	l.log(msg, logWarn, fields...)
}

// Error logs an error message.
func (l *stdoutLogger) Error(msg string, fields ...Fields) {
	l.log(msg, logError, fields...)
}

// New creates a new structured Logger.
// Supported levels: "debug", "info", "warn", "error"
func New(level string) Logger {
	return NewStdoutLogger(level)
}
