// Package logging builds the application's structured logger and the HTTP
// middleware that records requests. It is the single place that decides level
// and format, so every component's events land in one consistent stream.
package logging

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// New builds a slog logger writing to w. levelName is one of debug, info, warn,
// or error (case-insensitive; anything else means info). formatName is "json"
// for JSON lines, anything else for logfmt-style text.
func New(w io.Writer, levelName, formatName string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: Level(levelName)}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(formatName), "json") {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}
	return slog.New(handler)
}

// Level maps a level name to its slog.Level. Unknown names mean info.
func Level(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
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

// Discard returns a logger that drops every record. Components use it as their
// default so constructing one in a test stays silent until a logger is injected.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Middleware logs each HTTP request once it completes. It logs at debug so the
// discussion transcript, not page polling, dominates the default stream.
func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start),
			"remote", r.RemoteAddr,
		)
	})
}

// statusWriter records the status code and response size for Middleware.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}
