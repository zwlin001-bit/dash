package logx

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

type Level = slog.Level

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// Logger 封装 slog.Logger，强制经由 RedactHandler 保证敏感信息脱敏
type Logger struct {
	sl *slog.Logger
}

var (
	defaultMu     sync.RWMutex
	defaultLogger = New(os.Stderr, LevelInfo, "json")
)

// ParseLevel 将字符串转换为 slog.Level，默认 LevelInfo
func ParseLevel(lvl string) Level {
	switch strings.ToLower(strings.TrimSpace(lvl)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Init 全局初始化日志配置，一律输出到 stderr
// level: "debug" | "info" | "warn" | "error"
// format: "json" | "text"
func Init(level string, format string) {
	parsedLvl := ParseLevel(level)
	l := New(os.Stderr, parsedLvl, format)
	SetDefault(l)
}

// Default 返回当前全局默认 Logger
func Default() *Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultLogger
}

// SetDefault 替换全局默认 Logger
func SetDefault(l *Logger) {
	if l == nil {
		return
	}
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultLogger = l
}

// New 创建一个新的脱敏结构化 Logger
// format 支持 "json" 或 "text"（默认 "json"）
func New(w io.Writer, level Level, format ...string) *Logger {
	opts := &slog.HandlerOptions{
		Level: level,
	}

	fmtType := "json"
	if len(format) > 0 && strings.ToLower(strings.TrimSpace(format[0])) == "text" {
		fmtType = "text"
	}

	var inner slog.Handler
	if fmtType == "text" {
		inner = slog.NewTextHandler(w, opts)
	} else {
		inner = slog.NewJSONHandler(w, opts)
	}

	handler := &redactHandler{inner: inner}
	return &Logger{sl: slog.New(handler)}
}

// With 返回带有预设属性的 Logger
func (l *Logger) With(kv ...any) *Logger {
	return &Logger{sl: l.sl.With(sanitizeArgs(kv)...)}
}

// Debug 打印 Debug 级别日志
func (l *Logger) Debug(msg string, kv ...any) {
	l.sl.Debug(Redact(msg), sanitizeArgs(kv)...)
}

// Info 打印 Info 级别日志
func (l *Logger) Info(msg string, kv ...any) {
	l.sl.Info(Redact(msg), sanitizeArgs(kv)...)
}

// Warn 打印 Warn 级别日志
func (l *Logger) Warn(msg string, kv ...any) {
	l.sl.Warn(Redact(msg), sanitizeArgs(kv)...)
}

// Error 打印 Error 级别日志
func (l *Logger) Error(msg string, kv ...any) {
	l.sl.Error(Redact(msg), sanitizeArgs(kv)...)
}

// Slog 返回底层 slog.Logger
func (l *Logger) Slog() *slog.Logger {
	return l.sl
}

// 包级便捷函数，使用默认全局 Logger

func Debug(msg string, kv ...any) {
	Default().Debug(msg, kv...)
}

func Info(msg string, kv ...any) {
	Default().Info(msg, kv...)
}

func Warn(msg string, kv ...any) {
	Default().Warn(msg, kv...)
}

func Error(msg string, kv ...any) {
	Default().Error(msg, kv...)
}

func With(kv ...any) *Logger {
	return Default().With(kv...)
}

// redactHandler 拦截所有日志记录，对消息与所有属性进行脱敏
type redactHandler struct {
	inner slog.Handler
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	newRecord := slog.NewRecord(r.Time, r.Level, Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		newRecord.AddAttrs(sanitizeAttr(a))
		return true
	})
	return h.inner.Handle(ctx, newRecord)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		sanitized[i] = sanitizeAttr(a)
	}
	return &redactHandler{inner: h.inner.WithAttrs(sanitized)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: h.inner.WithGroup(name)}
}

// sanitizeAttr 对单个 slog.Attr 进行脱敏
func sanitizeAttr(a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		if isTokenKey(a.Key) {
			return slog.String(a.Key, RedactToken(a.Value.String()))
		}
		return slog.String(a.Key, "***")
	}

	if strings.EqualFold(a.Key, "dsn") && a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, RedactDSN(a.Value.String()))
	}

	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(a.Value.String()))
	case slog.KindGroup:
		attrs := a.Value.Group()
		sanitizedGroup := make([]slog.Attr, len(attrs))
		for i, child := range attrs {
			sanitizedGroup[i] = sanitizeAttr(child)
		}
		return slog.Group(a.Key, anySliceToAny(sanitizedGroup)...)
	default:
		return a
	}
}

func anySliceToAny(attrs []slog.Attr) []any {
	res := make([]any, len(attrs))
	for i, a := range attrs {
		res[i] = a
	}
	return res
}

// sanitizeArgs 对传入的 key-value 参数列表进行初步脱敏
func sanitizeArgs(args []any) []any {
	if len(args) == 0 {
		return args
	}

	sanitized := make([]any, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch v := arg.(type) {
		case slog.Attr:
			sanitized[i] = sanitizeAttr(v)
		case string:
			// 检查是否为 key-value 对中的 key
			if i%2 == 0 && i+1 < len(args) {
				sanitized[i] = v
				if isSensitiveKey(v) {
					if strVal, ok := args[i+1].(string); ok && isTokenKey(v) {
						sanitized[i+1] = RedactToken(strVal)
					} else {
						sanitized[i+1] = "***"
					}
					i++
				} else if strings.EqualFold(v, "dsn") {
					if strVal, ok := args[i+1].(string); ok {
						sanitized[i+1] = RedactDSN(strVal)
					} else {
						sanitized[i+1] = args[i+1]
					}
					i++
				} else if strVal, ok := args[i+1].(string); ok {
					sanitized[i+1] = Redact(strVal)
					i++
				} else {
					sanitized[i+1] = args[i+1]
					i++
				}
			} else {
				sanitized[i] = Redact(v)
			}
		default:
			sanitized[i] = arg
		}
	}
	return sanitized
}
