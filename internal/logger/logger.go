package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
}

type Log struct {
	minLevel Level
	std      *log.Logger
	fields   map[string]any
}

func New(level string) *Log {
	parsed, ok := parseLevel(level)
	if !ok {
		parsed = LevelInfo
	}
	return &Log{
		minLevel: parsed,
		std:      log.New(os.Stderr, "", 0),
		fields:   map[string]any{},
	}
}

func NewFromEnv() *Log {
	return New(os.Getenv("LOG_LEVEL"))
}

func IsValidLevel(level string) bool {
	_, ok := parseLevel(level)
	return ok
}

func NormalizeLevel(level string) string {
	parsed, ok := parseLevel(level)
	if !ok {
		return "info"
	}
	return strings.ToLower(levelNames[parsed])
}

func (l *Log) SetOutput(w io.Writer) {
	l.std.SetOutput(w)
}

func (l *Log) With(kv ...any) *Log {
	merged := make(map[string]any, len(l.fields)+(len(kv)/2))
	for k, v := range l.fields {
		merged[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key := strings.TrimSpace(fmt.Sprintf("%v", kv[i]))
		if key == "" {
			continue
		}
		merged[key] = kv[i+1]
	}
	return &Log{minLevel: l.minLevel, std: l.std, fields: merged}
}

func (l *Log) Debug(msg string, kv ...any) { l.write(LevelDebug, msg, kv...) }
func (l *Log) Info(msg string, kv ...any)  { l.write(LevelInfo, msg, kv...) }
func (l *Log) Warn(msg string, kv ...any)  { l.write(LevelWarn, msg, kv...) }
func (l *Log) Error(msg string, kv ...any) { l.write(LevelError, msg, kv...) }

func (l *Log) DebugLazy(fn func() string, kv ...any) {
	if !l.enabled(LevelDebug) {
		return
	}
	l.write(LevelDebug, fn(), kv...)
}

func (l *Log) enabled(level Level) bool {
	return level >= l.minLevel
}

func (l *Log) write(level Level, msg string, kv ...any) {
	if !l.enabled(level) {
		return
	}

	fields := make(map[string]any, len(l.fields)+(len(kv)/2))
	for k, v := range l.fields {
		fields[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key := strings.TrimSpace(fmt.Sprintf("%v", kv[i]))
		if key == "" {
			continue
		}
		fields[key] = kv[i+1]
	}

	l.std.Printf("[%s] [%s] [%s] %s", levelNames[level], time.Now().UTC().Format(time.RFC3339), formatFields(fields), msg)
}

func formatFields(fields map[string]any) string {
	if len(fields) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, fields[k]))
	}
	return strings.Join(parts, " ")
}

func parseLevel(level string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return LevelInfo, true
	case "debug":
		return LevelDebug, true
	case "warn":
		return LevelWarn, true
	case "error":
		return LevelError, true
	default:
		return LevelInfo, false
	}
}
