package util

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type LogLevel int

const (
	LevelInfo LogLevel = iota
	LevelDebug
	LevelTrace
)

var (
	level           = LevelInfo
	out   io.Writer = os.Stderr
	mu    sync.Mutex
)

func Configure(levelText string, writer io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	level = parseLevel(levelText)
	if writer != nil {
		out = writer
	}
}

func CurrentLevel() LogLevel {
	mu.Lock()
	defer mu.Unlock()
	return level
}

func Info(component, msg string, fields map[string]any) {
	log(LevelInfo, "INFO", component, msg, fields)
}

func Debug(component, msg string, fields map[string]any) {
	log(LevelDebug, "DEBUG", component, msg, fields)
}

func Trace(component, msg string, fields map[string]any) {
	log(LevelTrace, "TRACE", component, msg, fields)
}

func Warn(component, msg string, fields map[string]any) {
	log(LevelInfo, "WARN", component, msg, fields)
}

func Error(component, msg string, fields map[string]any) {
	log(LevelInfo, "ERROR", component, msg, fields)
}

func log(min LogLevel, text, component, msg string, fields map[string]any) {
	mu.Lock()
	current := level
	w := out
	mu.Unlock()

	if current < min {
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	parts := []string{ts, "level=" + text, "component=" + sanitize(component), "msg=" + quote(msg)}
	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, sanitize(k)+"="+quote(fmt.Sprint(fields[k])))
		}
	}
	_, _ = io.WriteString(w, strings.Join(parts, " ")+"\n")
}

func parseLevel(text string) LogLevel {
	s := strings.TrimSpace(strings.ToLower(text))
	switch s {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	default:
		return LevelInfo
	}
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "\n", "_")
	s = strings.ReplaceAll(s, "\t", "_")
	return s
}

func quote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return "\"" + s + "\""
}
