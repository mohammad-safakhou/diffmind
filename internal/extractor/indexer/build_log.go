package indexer

import (
	"strings"
	"sync"
)

// tailBuf is an io.Writer that retains only the last `cap` bytes. The
// stderr+stdout of `docker build` can run to many MB on a cold build;
// we keep enough trailing context to surface error tails without
// growing without bound.
type tailBuf struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= t.cap {
		t.buf = append(t.buf[:0], p[len(p)-t.cap:]...)
		return len(p), nil
	}
	if len(t.buf)+len(p) <= t.cap {
		t.buf = append(t.buf, p...)
		return len(p), nil
	}
	keep := t.cap - len(p)
	t.buf = append(t.buf[len(t.buf)-keep:], p...)
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// Reset clears the buffer. Used between BuildKit and legacy-fallback
// build attempts so the second attempt's log tail isn't polluted with
// the first attempt's stderr.
func (t *tailBuf) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = t.buf[:0]
}

// lastLines returns the last n lines of s, used to keep error messages
// from getting unwieldy when docker build fails in stage 60-of-76.
func lastLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return "..." + "\n" + strings.Join(lines[len(lines)-n:], "\n")
}
