package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// indexer encapsulates one SCIP indexer binary (scip-java,
// scip-typescript, ...). Implementations are pure side-effecting
// wrappers around `exec.Command`.
//
// CONTRACT
//
//   - Run MUST return one LanguageResult per element of in.Languages.
//     The Name field MUST be set to the canonical language identifier
//     (the same string the caller passed in).
//   - Run is expected to be safe to call concurrently with other
//     indexers operating on the same Source (which is mounted read-only)
//     but with disjoint Workdirs.
//   - Run is expected to populate IndexPath when Status == "ok". The
//     file at IndexPath must be a valid SCIP proto stream.
//   - Run MUST honor ctx cancellation: kill the subprocess and return
//     a "failed" result with the cancellation reason as Reason. The
//     orchestrator uses ctx timeouts to bound total runtime.
type indexer interface {
	Name() string
	Run(ctx context.Context, in indexerInput) []LanguageResult
}

// indexerInput is the fully-resolved per-invocation config.
type indexerInput struct {
	Source    string     // /sources (read-only)
	Workdir   string     // /output/work/<indexer-name> — writable, pre-created
	Languages []language // canonical languages this indexer is asked to handle
}

// indexerGroup pairs an indexer with the subset of requested languages
// it covers. Constructed by groupByIndexer().
type indexerGroup struct {
	Indexer   indexer
	Languages []language
}

// groupByIndexer maps a list of canonical languages to the minimum
// set of indexers needed to cover them. Several languages share an
// indexer (Java/Scala/Kotlin → scip-java; TypeScript/JavaScript →
// scip-typescript), so this dedupes those cases.
//
// Returned order is stable so the orchestrator schedules deterministically.
func groupByIndexer(langs []language) []indexerGroup {
	registry := indexerRegistry()
	// Map from indexer name → grouped languages, in stable order.
	type bucket struct {
		idx   indexer
		langs []language
	}
	buckets := []bucket{}
	seen := map[string]int{} // indexer name → index in buckets

	for _, l := range langs {
		idx := registry[l]
		if idx == nil {
			// Unknown language — orchestrator already validated, so
			// this is unreachable. Skip defensively.
			continue
		}
		if pos, ok := seen[idx.Name()]; ok {
			buckets[pos].langs = append(buckets[pos].langs, l)
			continue
		}
		seen[idx.Name()] = len(buckets)
		buckets = append(buckets, bucket{idx: idx, langs: []language{l}})
	}

	out := make([]indexerGroup, len(buckets))
	for i, b := range buckets {
		out[i] = indexerGroup{Indexer: b.idx, Languages: b.langs}
	}
	return out
}

// indexerRegistry maps each supported language to the indexer that
// owns it. The map is rebuilt on every call so tests can swap
// implementations by overriding indexerFactories.
//
// Returning a map (not a slice) ensures O(1) lookups in groupByIndexer.
func indexerRegistry() map[language]indexer {
	javaIdx := newScipJava()
	tsIdx := newScipTypeScript()
	pyIdx := newScipPython()
	goIdx := newScipGo()
	rubyIdx := newScipRuby()
	cppIdx := newScipClang()
	dotnetIdx := newScipDotnet()

	return map[language]indexer{
		langJava:       javaIdx,
		langScala:      javaIdx,
		langKotlin:     javaIdx, // scip-java handles Kotlin via the semanticdb-kotlinc plugin under Gradle
		langTypeScript: tsIdx,
		langJavaScript: tsIdx,
		langPython:     pyIdx,
		langGo:         goIdx,
		langRuby:       rubyIdx,
		langCPP:        cppIdx,
		langC:          cppIdx,
		langCSharp:     dotnetIdx,
	}
}

// ---------------------------------------------------------------------
// runProcess: shared subprocess helper used by every indexer.
//
// Captures stdout, tail of stderr (last ~stderrTail bytes), and the
// elapsed wall-clock time. Returns the exit error verbatim so callers
// can distinguish "process exited non-zero" from "context cancelled
// before start" or "executable not found".
// ---------------------------------------------------------------------

// stderrTail is how many bytes of trailing stderr we keep on failure.
// We use a ring buffer so a very chatty indexer (scip-typescript can
// be) doesn't blow up the report. 16 KB is plenty for the actual error
// message at the tail.
const stderrTail = 16 * 1024

func runProcess(ctx context.Context, dir string, name string, args ...string) (stdout []byte, stderr []byte, dur time.Duration, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ() // inherit everything; indexers need PATH, JAVA_HOME, etc.

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf

	tail := newTailBuffer(stderrTail)
	cmd.Stderr = tail

	start := time.Now()
	err = cmd.Run()
	dur = time.Since(start)
	return outBuf.Bytes(), tail.Bytes(), dur, err
}

// tailBuffer is an io.Writer that retains only the last N bytes
// written. We use it to bound the stderr footprint of chatty indexers.
type tailBuffer struct {
	buf []byte
	cap int
}

func newTailBuffer(capBytes int) *tailBuffer {
	return &tailBuffer{cap: capBytes}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	if len(p) >= t.cap {
		t.buf = append(t.buf[:0], p[len(p)-t.cap:]...)
		return len(p), nil
	}
	if len(t.buf)+len(p) <= t.cap {
		t.buf = append(t.buf, p...)
		return len(p), nil
	}
	// Overflow: drop oldest, keep newest.
	keep := t.cap - len(p)
	t.buf = append(t.buf[len(t.buf)-keep:], p...)
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte { return t.buf }

// trimError prepares an indexer's trailing stderr for inclusion in the
// JSON report. Empty/whitespace-only output collapses to "".
func trimError(stderr []byte) string {
	return strings.TrimSpace(string(stderr))
}

// fanOutLanguages builds one LanguageResult per requested language
// from a single indexer outcome. Several indexers (scip-java,
// scip-typescript) handle multiple languages with one invocation; we
// still want one row per language in the report.
//
// The IndexPath, Status, Indexer, Duration, Reason and Error fields
// are duplicated across the rows. Only the Name is per-language. This
// is a deliberate denormalisation: the report is consumed once by the
// host and never queried, so duplication beats joining at read time.
func fanOutLanguages(langs []language, indexerName string, template LanguageResult) []LanguageResult {
	out := make([]LanguageResult, 0, len(langs))
	for _, l := range langs {
		row := template
		row.Name = string(l)
		row.Indexer = indexerName
		out = append(out, row)
	}
	return out
}

// outputPath returns the absolute path inside the indexer's workdir
// where the per-language index file is written. Indexers append this
// to their CLI as `--output` (or equivalent).
func outputPath(workdir, indexerName string) string {
	return filepath.Join(workdir, indexerName+".scip")
}

// ensureDir creates the directory if missing. Errors are logged but
// non-fatal: the indexer will likely fail with a clearer message.
func ensureDir(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logf("ensure dir %s: %v", dir, err)
	}
}

// statSize returns the file size in bytes or 0 if the file is missing.
func statSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// captureBytes is a small helper to drain a Reader into memory. Used
// when an indexer writes its output to a pipe instead of a file.
func captureBytes(r io.Reader, max int) []byte {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			if len(buf)+n > max {
				buf = append(buf, tmp[:max-len(buf)]...)
				return buf
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf
		}
	}
}

// _ ensure imports stay used (captureBytes is a future-proof helper
// not yet called by any indexer; once scip-clang or scip-ruby grow
// streaming variants we'll wire them in).
var _ = fmt.Sprintf
var _ = captureBytes
