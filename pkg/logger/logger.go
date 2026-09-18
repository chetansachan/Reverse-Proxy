package logger

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"
)

var defaultLogger *slog.Logger

func init() {
	// Default to structured text logger writing to stdout
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger)
}

// SetLevel updates the logger level.
func SetLevel(level slog.Level) {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

// ResponseRecorder captures the status code written by downstream handlers.
type ResponseRecorder struct {
	http.ResponseWriter
	StatusCode int
	BytesWritten int64
}

func (rec *ResponseRecorder) WriteHeader(code int) {
	rec.StatusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *ResponseRecorder) Write(b []byte) (int, error) {
	n, err := rec.ResponseWriter.Write(b)
	rec.BytesWritten += int64(n)
	return n, err
}

// HTTPMiddleware wraps an http.Handler with structured access logging.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &ResponseRecorder{
			ResponseWriter: w,
			StatusCode:     http.StatusOK,
		}

		next.ServeHTTP(rec, r)

		duration := time.Since(start)
		defaultLogger.InfoContext(r.Context(), "http_request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
			slog.Int("status", rec.StatusCode),
			slog.Int64("bytes", rec.BytesWritten),
			slog.Duration("duration", duration),
		)
	})
}

// WithContext returns a logger enriched from context.
func FromContext(ctx context.Context) *slog.Logger {
	return defaultLogger
}
