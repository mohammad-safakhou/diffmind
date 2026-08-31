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

// LogLevel controls verbosity.
type LogLevel int

const (
	LevelInfo  LogLevel = 0
	LevelDebug LogLevel = 1
	LevelTrace LogLevel = 2
)

// Logger is a simple structured logger (no external deps).
type Logger struct {
	mu      sync.Mutex
	level   LogLevel
	writers []io.Writer
}

// NewLogger creates a logger that writes to stderr.
func NewLogger(level LogLevel) *Logger {
	return &Logger{level: level, writers: []io.Writer{os.Stderr}}
}

// AddWriter adds an additional writer (e.g. a log file).
func (l *Logger) AddWriter(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writers = append(l.writers, w)
}

// Info logs at info level.
func (l *Logger) Info(msg string, kv ...string) { l.log(LevelInfo, "INF", msg, kv) }

// Debug logs at debug level.
func (l *Logger) Debug(msg string, kv ...string) { l.log(LevelDebug, "DBG", msg, kv) }

// Trace logs at trace level.
func (l *Logger) Trace(msg string, kv ...string) { l.log(LevelTrace, "TRC", msg, kv) }

// Warn logs at info level with a WRN tag.
func (l *Logger) Warn(msg string, kv ...string) { l.log(LevelInfo, "WRN", msg, kv) }

// Error logs at info level with an ERR tag.
func (l *Logger) Error(msg string, kv ...string) { l.log(LevelInfo, "ERR", msg, kv) }

func (l *Logger) log(lvl LogLevel, tag, msg string, kv []string) {
	if lvl > l.level {
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var sb strings.Builder
	sb.WriteString(ts)
	sb.WriteString(" [")
	sb.WriteString(tag)
	sb.WriteString("] ")
	sb.WriteString(msg)

	if len(kv) >= 2 {
		pairs := make([]string, 0, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			pairs = append(pairs, kv[i]+"="+kv[i+1])
		}
		sort.Strings(pairs)
		sb.WriteString(" ")
		sb.WriteString(strings.Join(pairs, " "))
	}
	sb.WriteString("\n")

	line := sb.String()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, w := range l.writers {
		fmt.Fprint(w, line)
	}
}
