package logging

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":    slog.LevelDebug,
		"INFO":     slog.LevelInfo,
		" warn ":   slog.LevelWarn,
		"warning":  slog.LevelWarn,
		"error":    slog.LevelError,
		"":         slog.LevelInfo,
		"nonsense": slog.LevelInfo,
	}
	for name, want := range cases {
		if got := Level(name); got != want {
			t.Errorf("Level(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNewFormat(t *testing.T) {
	var text bytes.Buffer
	New(&text, "info", "text").Info("hello", "k", "v")
	if !strings.Contains(text.String(), "msg=hello") || !strings.Contains(text.String(), "k=v") {
		t.Errorf("text output = %q, want logfmt-style record", text.String())
	}

	var jsonOut bytes.Buffer
	New(&jsonOut, "info", "json").Info("hello", "k", "v")
	if !strings.HasPrefix(strings.TrimSpace(jsonOut.String()), "{") || !strings.Contains(jsonOut.String(), `"msg":"hello"`) {
		t.Errorf("json output = %q, want a JSON record", jsonOut.String())
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "warn", "text").Info("hidden")
	if buf.Len() != 0 {
		t.Errorf("info record written at warn level: %q", buf.String())
	}
}

func TestMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "debug", "text")
	handler := Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("hi"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/discussion.html", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	out := buf.String()
	for _, want := range []string{"msg=\"http request\"", "status=418", "bytes=2", "path=/discussion.html"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output %q missing %q", out, want)
		}
	}
}
