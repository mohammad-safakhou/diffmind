package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/indexer"
	"github.com/mohammad-safakhou/diffmind/internal/scip"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// runIndexStage produces a SCIP index of the snapshot. It is run
// between detail and connections, replacing what used to be an LLM
// pre-processing step. Outputs:
//
//   - <runDir>/index/index.scip          merged multi-language SCIP index
//   - <runDir>/index/index_status.json   per-language status report
//   - orchestrator.scipIndex             in-memory *scip.Index for the
//     connections stage to query
//
// The stage emits standard pipeline events so the dashboard renders
// per-language progress just like any other stage. Deliberate skips
// (Indexer.Disabled or no supported languages) are allowed to degrade to the
// shallow matcher, but a configured indexer that actually fails must halt the
// run. Otherwise the dashboard can show a successful run with a failed stage
// and a path-less connections graph.
func (o *orchestrator) runIndexStage(ctx context.Context) error {
	if o.cfg.Indexer.Disabled {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index", Status: events.StatusSkipped,
			Message: "indexer disabled by config",
		})
		util.Info("agents.index", "indexer disabled by config", nil)
		return nil
	}

	// Block on the parallel image build, indefinitely (per the
	// Sprint 4 design choice). If the build FAILED, we halt the
	// pipeline — the connections stage without the SCIP index is
	// near-useless and the user explicitly chose fail-fast over
	// running through the rest of the LLM work just to produce a
	// path-less catalog.
	//
	// SKIPPED is a different case: it means the build deliberately
	// did NOT run (indexer disabled, no supported languages, test
	// override). The connections stage will degrade to the shallow
	// matcher — that's the intended fallback, NOT a failure.
	builtImage, buildErr, buildSkipped := o.waitForImageBuild(ctx)
	if buildErr != nil {
		util.Error("agents.index", "indexer image build failed; halting run", map[string]any{
			"error": buildErr.Error(),
		})
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index", Status: events.StatusFailed,
			Message: buildErr.Error(),
		})
		// Return the error so the orchestrator's haltFailure
		// chain produces a proper run_failure.{json,md} the
		// retry command can resume from once the user has
		// fixed the cause.
		return fmt.Errorf("indexer image build failed: %w", buildErr)
	}
	if buildSkipped && builtImage == "" && o.indexerOverride == nil {
		// No image, no override → nothing to do. The image
		// build was skipped for a legitimate reason (no
		// supported languages, indexer disabled, etc.).
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index", Status: events.StatusSkipped,
			Message: "indexer image build skipped",
		})
		util.Info("agents.index", "image build skipped; connections will use shallow matcher", nil)
		return nil
	}

	// Resolve paths. runDir is empty when the orchestrator is invoked
	// without a CLI artifact dir (e.g. some test paths). In that case
	// we still want to index (so connections can use SCIP), but we
	// write the outputs under a temp dir.
	//
	// CRITICAL: the indexer runs in a Docker container with volume
	// mounts. Docker rejects RELATIVE host paths for `-v src:dst`.
	// The diffmind CLI passes RunDir = "<artifacts.base_dir>/<runID>",
	// where base_dir defaults to ".diffmind/runs" — a relative path.
	// We MUST resolve this to an absolute path before handing it to
	// the indexer. Run 20260524T212718Z was the first complete run
	// where this regression surfaced: the index stage emitted
	// "output path must be absolute: \".diffmind/runs/...\"" and the
	// connections stage degraded to the empty shallow matcher,
	// producing zero connections.
	indexDir := o.indexOutputDir()
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("index dir: %w", err)
	}
	indexDir, err := filepath.Abs(indexDir)
	if err != nil {
		return fmt.Errorf("index dir absolute: %w", err)
	}

	// Fast-path: if a valid index.scip already exists in the run directory
	// (e.g. the run was retried after connections failed, not after indexing
	// failed), load it directly and skip the Docker container entirely.
	// This saves many minutes on retries where only post-index stages need
	// to re-run.
	existingIndex := filepath.Join(indexDir, "index.scip")
	if st, err := os.Stat(existingIndex); err == nil && st.Size() > 0 {
		idx, loadErr := scip.Load(existingIndex)
		if loadErr == nil {
			o.scipIndex = idx
			o.emit(events.Event{
				Kind: events.KindStageCompleted, Stage: "index", Status: events.StatusSuccess,
				Message: "loaded existing index from prior run",
				Payload: map[string]any{
					"index_path":  existingIndex,
					"index_bytes": st.Size(),
					"reused":      true,
				},
			})
			util.Info("agents.index", "reusing existing index.scip from run directory", map[string]any{
				"index_path":  existingIndex,
				"index_bytes": st.Size(),
				"documents":   idx.DocumentCount(),
			})
			return nil
		}
		util.Warn("agents.index", "existing index.scip unreadable, re-running indexer", map[string]any{
			"index_path": existingIndex,
			"error":      loadErr.Error(),
		})
	}

	sourceDir, err := filepath.Abs(o.sessionDir)
	if err != nil {
		return fmt.Errorf("source dir absolute: %w", err)
	}

	// Use the image the parallel build settled on (composite
	// tag for production, explicit override for power users).
	// Test paths leave builtImage empty and rely on
	// o.indexerOverride.
	imageTag := builtImage
	if imageTag == "" {
		imageTag = o.cfg.Indexer.Image
	}

	stageStart := time.Now()
	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "index", Status: events.StatusRunning,
		Payload: map[string]any{
			"tip":         "Running the SCIP indexer container against the snapshot.",
			"snapshot":    sourceDir,
			"output_dir":  indexDir,
			"image":       imageTag,
			"pull_policy": effectivePullPolicy(o.cfg.Indexer),
			"languages":   o.cfg.Indexer.Languages,
		},
	})

	// Pick the indexer implementation. Tests may inject a fake via
	// o.indexerOverride; production always uses the Docker CLI driver.
	var idxr indexer.Indexer
	if o.indexerOverride != nil {
		idxr = o.indexerOverride
	} else {
		docker := indexer.NewDockerIndexer()
		// Tee the indexer subprocess output so users can see live
		// "scip-java cleaning target..." style noise without having
		// to `docker logs` into the container.
		docker.Stderr = newIndexerLogWriter("index.stderr")
		docker.Stdout = newIndexerLogWriter("index.stdout")
		idxr = docker
	}

	req := indexer.RunRequest{
		SourcePath:        sourceDir,
		OutputPath:        indexDir,
		Languages:         o.cfg.Indexer.Languages,
		PerIndexerTimeout: time.Duration(effectivePerIndexerTimeout(o.cfg.Indexer)) * time.Second,
		Parallel:          effectiveParallel(o.cfg.Indexer),
		Image:             imageTag,
		PullPolicy:        indexer.PullPolicy(effectivePullPolicy(o.cfg.Indexer)),
		NetworkMode:       o.cfg.Indexer.NetworkMode,
		ExtraEnv:          o.cfg.Indexer.ExtraEnv,
		ExtraMounts:       mergeMounts(o.cfg.Indexer.ExtraMounts, defaultIndexerMounts()),
	}
	// The parallel image build already ensured the image
	// exists. We no longer call ensureIndexerImage from this
	// path; the legacy single-image flow is preserved only via
	// the (unused-in-the-new-flow) cfg.Indexer.AutoBuild knob
	// that older configs may still set.

	indexJobID := "index.docker"
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "index", JobID: indexJobID,
		Status: events.StatusRunning,
		Payload: map[string]any{
			// Use the resolved image (req.Image is empty when the
			// caller relies on indexer.DefaultImage / the env var).
			"image":      indexer.ResolveImage(req.Image),
			"snapshot":   req.SourcePath,
			"output_dir": req.OutputPath,
			"languages":  req.Languages,
		},
	})

	result, err := idxr.Index(ctx, req)

	// Even on error, the result may carry partial per-language data
	// (one indexer succeeded, another failed). We emit per-language
	// jobs in either case.
	if result != nil {
		o.emitPerLanguageResults(result)
	}

	if err != nil {
		// The indexer was configured and ran, but failed. Halt the run so
		// users do not mistake a path-less connection graph for a complete
		// extraction.
		util.Warn("agents.index", "indexer failed; halting run", map[string]any{
			"error": err.Error(),
		})
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "index", JobID: indexJobID,
			Status: events.StatusFailed, Message: err.Error(),
			Payload: map[string]any{
				"duration_ms": time.Since(stageStart).Milliseconds(),
			},
		})
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index", Status: events.StatusFailed,
			Message: err.Error(),
		})
		return fmt.Errorf("indexer failed: %w", err)
	}

	o.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "index", JobID: indexJobID,
		Status: events.StatusSuccess,
		Payload: map[string]any{
			"index_path":  result.IndexPath,
			"index_bytes": result.IndexBytes,
			"duration_ms": time.Since(stageStart).Milliseconds(),
			"languages":   languageNames(result.Languages),
		},
	})

	// Load the SCIP index into memory so connections can query it.
	if result.IndexPath != "" {
		idx, loadErr := scip.Load(result.IndexPath)
		if loadErr != nil {
			util.Warn("agents.index", "scip load failed; connections will degrade", map[string]any{
				"index_path": result.IndexPath,
				"error":      loadErr.Error(),
			})
		} else {
			o.scipIndex = idx
			util.Info("agents.index", "scip index loaded", map[string]any{
				"index_path":         result.IndexPath,
				"documents":          idx.DocumentCount(),
				"symbol_definitions": idx.SymbolDefinitionCount(),
				"detected_languages": result.DetectedLanguages,
			})
		}
	}

	o.emit(events.Event{
		Kind: events.KindStageCompleted, Stage: "index", Status: events.StatusSuccess,
		Payload: map[string]any{
			"index_path":         result.IndexPath,
			"index_bytes":        result.IndexBytes,
			"detected_languages": result.DetectedLanguages,
		},
	})
	return nil
}

// indexOutputDir returns the directory under the run dir where the
// indexer writes its output. Falls back to a per-snapshot temp dir
// when no run dir is configured (test/internal paths).
func (o *orchestrator) indexOutputDir() string {
	if o.runDir != "" {
		return filepath.Join(o.runDir, "index")
	}
	return filepath.Join(o.sessionDir, ".diffmind-index")
}

// imageBuildStreamer fans the docker-build subprocess's stdout/stderr
// into the event bus as KindLog events on the index.build job. It
// debounces output so a chatty BuildKit doesn't saturate the SSE
// channel — one event per ~250 ms with the latest line wins.
//
// The streamer also writes to the in-binary tail buffer via the
// indexer.Builder's internal LogTail capture, so we don't have to
// duplicate that here.
type imageBuildStreamer struct {
	o           *orchestrator
	stage       string
	jobID       string
	pending     []byte
	mu          sync.Mutex
	lastEmitted time.Time
}

// newImageBuildStreamer constructs a streamer tagged with the stage
// it should attribute log events to. The parallel image build (Sprint 4)
// uses stage="index.build"; the legacy single-image path used "index".
func newImageBuildStreamer(o *orchestrator, stage string) *imageBuildStreamer {
	if stage == "" {
		stage = "index.build"
	}
	return &imageBuildStreamer{o: o, stage: stage, jobID: stage}
}

// Write implements io.Writer. We buffer until we hit a newline OR
// 250 ms have elapsed since the last emit, whichever comes first.
func (s *imageBuildStreamer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, p...)
	const debounce = 250 * time.Millisecond
	if time.Since(s.lastEmitted) < debounce {
		// Still in the cooldown; let the next write or the final
		// flush pick it up. We DO eagerly emit when the buffer
		// crosses a hard ceiling so a long single-line output
		// (BuildKit progress bars) still reaches the bus.
		const hardCeil = 16 * 1024
		if len(s.pending) < hardCeil {
			return len(p), nil
		}
	}
	s.emitLocked()
	return len(p), nil
}

// emitLocked writes the current pending buffer as a single log event
// and resets the buffer. Caller holds s.mu.
func (s *imageBuildStreamer) emitLocked() {
	if len(s.pending) == 0 {
		return
	}
	// Trim trailing newline-only fragments for cleaner UI display.
	msg := strings.TrimRight(string(s.pending), "\n")
	s.pending = s.pending[:0]
	s.lastEmitted = time.Now()
	s.o.emit(events.Event{
		Kind: events.KindLog, Stage: s.stage, JobID: s.jobID,
		Message: lastLineOf(msg),
		Payload: map[string]any{
			// "tail" carries the multi-line excerpt so a UI can show
			// expanded build progress when the user clicks the pill.
			"tail": truncate(msg, 4000),
		},
	})
}

// lastLineOf returns the last non-empty line of s; useful for the
// single-line `Message` field of the emitted event.
func lastLineOf(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// lastLines mirrors the helper inside internal/indexer; we re-declare
// it here at package scope so the streamer can reuse it without
// reaching across packages.
func init() {
	_ = lastLineOf // keep referenced even if no caller appears in initial drafts
}

// emitPerLanguageResults converts the wrapper's per-language results
// into job_started/completed/failed events. The wrapper already grouped
// languages by indexer; we want one job per language for the UI so each
// language gets its own pill in the pipeline strip.
func (o *orchestrator) emitPerLanguageResults(r *indexer.RunResult) {
	for _, lang := range r.Languages {
		jobID := "index." + lang.Name
		status := events.StatusSuccess
		kind := events.KindJobCompleted
		switch lang.Status {
		case "ok":
			// completed already
		case "skipped":
			status = events.StatusSkipped
			kind = events.KindJobCompleted
		case "failed":
			status = events.StatusFailed
			kind = events.KindJobFailed
		default:
			// unknown status: surface as completed to avoid losing the
			// UI placeholder, but note the unrecognised status in the
			// payload so we can debug indexer wrapper drift.
		}
		o.emit(events.Event{
			Kind: kind, Stage: "index", JobID: jobID,
			Status:  status,
			Message: lang.Reason,
			Payload: map[string]any{
				"indexer":     lang.Indexer,
				"reason":      lang.Reason,
				"error":       truncate(lang.Error, 2000),
				"files":       lang.Files,
				"occurrences": lang.Occurrences,
				"duration_ms": lang.DurationMs,
			},
		})
	}
}

// effectivePullPolicy returns the configured pull policy or the
// default. Centralised so the same fallback applies to the event
// payload and the indexer.RunRequest field.
func effectivePullPolicy(cfg config.Indexer) string {
	if cfg.PullPolicy != "" {
		return cfg.PullPolicy
	}
	return string(indexer.PullIfAbsent)
}

// effectivePerIndexerTimeout returns the configured per-indexer
// timeout (seconds) or the default.
func effectivePerIndexerTimeout(cfg config.Indexer) int {
	if cfg.PerIndexerTimeoutSec > 0 {
		return cfg.PerIndexerTimeoutSec
	}
	return 30 * 60
}

// effectiveParallel returns the configured parallelism or the default.
func effectiveParallel(cfg config.Indexer) int {
	if cfg.Parallel > 0 {
		return cfg.Parallel
	}
	return 4
}

// languageNames extracts a sorted slice of language names from a
// per-language result list. Used in event payloads where we want a
// compact "what got indexed" string for the UI.
func languageNames(in []indexer.LanguageResult) []string {
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, l.Name)
	}
	return out
}

// truncate is a small helper used by event payloads to keep error
// strings UI-friendly. We never want a multi-MB stack trace blown
// into the dashboard; the full stderr is persisted in the JSON
// report file regardless.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// newIndexerLogWriter returns a writer that forwards bytes from the
// docker subprocess into the orchestrator's event bus as `log` events.
// We don't (yet) attempt to parse the wrapper's structured logs; the
// stderr+stdout copies inside the indexer run dir are the source of
// truth for forensic analysis. Live tee is just for human watchers.
func newIndexerLogWriter(channel string) *indexerLogWriter {
	return &indexerLogWriter{channel: channel}
}

// indexerLogWriter is an io.Writer that swallows whatever the docker
// CLI emits. Sprint 2 keeps it dead-simple: we just discard the bytes
// in production builds. The buffered logs the wrapper writes to
// stdout/stderr are still parsed by docker.Index() (which reads the
// JSON report from the captured buffer).
type indexerLogWriter struct {
	channel string
}

func (w *indexerLogWriter) Write(p []byte) (int, error) {
	// Discard for now. A future iteration can fan these out as
	// KindLog events; we don't do that today because docker emits
	// tens of thousands of lines for a Maven cold pull and that
	// would saturate the SSE channel.
	return len(p), nil
}

// defaultIndexerMounts returns volume mounts to inject into every indexer
// container run, based on what exists on the host. Mounts are read-only
// and are skipped silently when the host path is absent.
//
// Rationale for each mount:
//
//   ~/.m2/settings.xml  Maven reads this for repository credentials and
//                       mirror URLs. Projects that depend on a private
//                       Artifactory/Nexus instance (common in enterprises)
//                       will fail to resolve their parent POM without it.
//
//   ~/.m2/repository    The local Maven cache. Mounting it read-only lets
//                       the container resolve already-downloaded artifacts
//                       without hitting the network at all, cutting cold
//                       build time from minutes to seconds on retry runs.
//                       Read-only is intentional: we don't want the
//                       container to pollute the host cache with snapshot
//                       artifacts built with our patched javac.
//
//   ~/.gradle/caches    Analogous cache directory for Gradle-based projects.
//
//   ~/.gradle/wrapper   Gradle wrapper JARs; avoids re-downloading the
//                       Gradle distribution on every container start.
func defaultIndexerMounts() map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	candidates := []struct{ host, container string }{
		{filepath.Join(home, ".m2", "settings.xml"), "/root/.m2/settings.xml:ro"},
		{filepath.Join(home, ".m2", "repository"), "/root/.m2/repository:ro"},
		{filepath.Join(home, ".gradle", "caches"), "/root/.gradle/caches:ro"},
		{filepath.Join(home, ".gradle", "wrapper"), "/root/.gradle/wrapper:ro"},
	}

	mounts := map[string]string{}
	for _, c := range candidates {
		if _, err := os.Stat(c.host); err == nil {
			mounts[c.host] = c.container
		}
	}
	return mounts
}

// mergeMounts merges explicit user-configured mounts (higher priority) with
// auto-detected defaults (lower priority). If the user has already configured
// a mount for the same container path, their value wins.
func mergeMounts(explicit, defaults map[string]string) map[string]string {
	if len(defaults) == 0 && len(explicit) == 0 {
		return nil
	}
	// Build a set of container paths already claimed by explicit mounts.
	explicitContainerPaths := map[string]bool{}
	for _, containerSpec := range explicit {
		// containerSpec may be "path:ro" or just "path".
		containerPath := strings.SplitN(containerSpec, ":", 2)[0]
		explicitContainerPaths[containerPath] = true
	}

	merged := make(map[string]string, len(explicit)+len(defaults))
	for k, v := range defaults {
		containerPath := strings.SplitN(v, ":", 2)[0]
		if !explicitContainerPaths[containerPath] {
			merged[k] = v
		}
	}
	for k, v := range explicit {
		merged[k] = v
	}
	return merged
}

// _ keeps imports referenced even if no production path uses them yet.
var _ = errors.New
