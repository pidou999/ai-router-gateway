// Package logger 提供结构化日志能力，基于标准库 slog。
// 所有日志自动携带 request_id 字段（来自 context），便于排障时串联请求链路。
package logger

import (
	"context"
	"log/slog"
	"os"
	"sync"
)

const requestIDKey = "request_id"

var (
	rootMu sync.Mutex
	root   *slog.Logger
)

func getRoot() *slog.Logger {
	rootMu.Lock()
	defer rootMu.Unlock()
	if root == nil {
		root = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:     slog.LevelInfo,
			AddSource: false,
		}))
	}
	return root
}

// Init 初始化全局 logger，可在 main 中调用以指定日志级别。
// level 为 "debug"/"info"/"warn"/"error"，默认 "info"。
func Init(level string) {
	l := slog.LevelInfo
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	}
	rootMu.Lock()
	defer rootMu.Unlock()
	root = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     l,
		AddSource: false,
	}))
}

// WithRequestID 返回一个附带 request_id 的新 logger（不影响原 logger）。
func WithRequestID(reqID string) *slog.Logger {
	return getRoot().With(requestIDKey, reqID)
}

// FromContext 从 context 中获取已绑定的 logger，不存在时返回 root。
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return getRoot()
	}
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return getRoot()
}

// IntoContext 把 logger 写入 context，供下游 handler/引擎使用。
func IntoContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// Fatal 输出 fatal 日志后调用 os.Exit(1)。
func Fatal(msg string, args ...any) {
	getRoot().Error(msg, args...)
	os.Exit(1)
}

// Fatalf 格式化输出 fatal 日志后调用 os.Exit(1)。
func Fatalf(format string, args ...any) {
	Fatal(format, args...)
}

// Error 输出 error 级别结构化日志。
func Error(msg string, args ...any) {
	getRoot().Error(msg, args...)
}

// Info 输出 info 级别结构化日志。
func Info(msg string, args ...any) {
	getRoot().Info(msg, args...)
}

// Warn 输出 warn 级别结构化日志。
func Warn(msg string, args ...any) {
	getRoot().Warn(msg, args...)
}

// Debug 输出 debug 级别结构化日志。
func Debug(msg string, args ...any) {
	getRoot().Debug(msg, args...)
}

type loggerKey struct{}
