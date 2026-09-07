package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
)

var (
	defaultLogger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Matches user:password@host (e.g. oracle://user:pass@host or root:pass@tcp(host))
	dsnPasswordPattern = regexp.MustCompile(`(?i)(://|^|\s)([a-zA-Z0-9_.-]+):([^:@/\s]+)@`)
	// Matches key=value / key: value for sensitive keys
	kvPasswordPattern = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token)\s*([:=])\s*([^\s,;&"']+)`)
)

// Redact sanitizes sensitive information such as passwords and secrets from strings.
func Redact(s string) string {
	if s == "" {
		return ""
	}
	// Redact user:password@host
	res := dsnPasswordPattern.ReplaceAllString(s, "${1}${2}:***@")
	// Redact key=val / key: val
	res = kvPasswordPattern.ReplaceAllString(res, "${1}${2}***")
	return res
}

// RedactConfig converts a config object to string with sensitive fields masked.
func RedactConfig(cfg any) string {
	str := fmt.Sprintf("%+v", cfg)
	return Redact(str)
}

// Info logs an informational message to stderr.
func Info(msg string, args ...any) {
	defaultLogger.Info(Redact(msg), args...)
}

// Error logs an error message to stderr.
func Error(msg string, args ...any) {
	defaultLogger.Error(Redact(msg), args...)
}

// Warn logs a warning message to stderr.
func Warn(msg string, args ...any) {
	defaultLogger.Warn(Redact(msg), args...)
}

// Debug logs a debug message to stderr.
func Debug(msg string, args ...any) {
	defaultLogger.Debug(Redact(msg), args...)
}

// InfoContext logs an informational message with context.
func InfoContext(ctx context.Context, msg string, args ...any) {
	defaultLogger.InfoContext(ctx, Redact(msg), args...)
}

// ErrorContext logs an error message with context.
func ErrorContext(ctx context.Context, msg string, args ...any) {
	defaultLogger.ErrorContext(ctx, Redact(msg), args...)
}
