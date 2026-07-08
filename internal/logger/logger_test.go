package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogLevelFiltering(t *testing.T) {
	l := New("warn")
	var buf bytes.Buffer
	l.SetOutput(&buf)

	l.Info("info message")
	l.Warn("warn message")
	l.Error("error message")

	out := buf.String()
	if strings.Contains(out, "info message") {
		t.Fatalf("info message should be filtered out: %q", out)
	}
	if !strings.Contains(out, "warn message") || !strings.Contains(out, "error message") {
		t.Fatalf("warn/error messages should be logged: %q", out)
	}
}

func TestStructuredContextOutput(t *testing.T) {
	l := New("debug").With("component", "auth", "user", "alice@example.com")
	var buf bytes.Buffer
	l.SetOutput(&buf)

	l.Info("login ok", "remote", "127.0.0.1")

	out := buf.String()
	if !strings.Contains(out, "[INFO]") {
		t.Fatalf("missing INFO level: %q", out)
	}
	if !strings.Contains(out, "component=auth") || !strings.Contains(out, "user=alice@example.com") || !strings.Contains(out, "remote=127.0.0.1") {
		t.Fatalf("missing structured context fields: %q", out)
	}
}

func TestDebugLazySkipsEvaluationWhenDisabled(t *testing.T) {
	l := New("error")
	called := false
	l.DebugLazy(func() string {
		called = true
		return "expensive"
	})
	if called {
		t.Fatal("lazy debug function should not be evaluated when debug logs are disabled")
	}
}
