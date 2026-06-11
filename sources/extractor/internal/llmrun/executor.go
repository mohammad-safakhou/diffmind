package llmrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type ExecutorOptions struct {
	Client     Client
	Verbose    VerbosePrompter
	Tokens     TokenReader
	Sessions   *SessionManager
	Captures   CaptureStore
	Totals     *TokenTotals
	Sink       events.Sink
	Directory  string
	RetryCount int
	Liveness   LivenessConfig
}

// Executor is the sole owner of one logical LLM call. It preserves the
// provider-facing lifecycle around retries, sessions, liveness, captures,
// fallback decoding, token reads, and events.
type Executor struct {
	client    Client
	verbose   VerbosePrompter
	tokens    TokenReader
	sessions  *SessionManager
	captures  CaptureStore
	totals    *TokenTotals
	sink      events.Sink
	directory string
	retries   int
	liveness  LivenessConfig
}

func NewExecutor(options ExecutorOptions) *Executor {
	retries := options.RetryCount
	if retries < 0 {
		retries = 0
	}
	sink := options.Sink
	if sink == nil {
		sink = events.NoopSink{}
	}
	totals := options.Totals
	if totals == nil {
		totals = &TokenTotals{}
	}
	sessions := options.Sessions
	if sessions == nil {
		sessions = NewSessionManager(SessionOptions{
			Client: options.Client, Sink: sink, Directory: options.Directory,
		})
	}
	return &Executor{
		client:    options.Client,
		verbose:   options.Verbose,
		tokens:    options.Tokens,
		sessions:  sessions,
		captures:  options.Captures,
		totals:    totals,
		sink:      sink,
		directory: options.Directory,
		retries:   retries,
		liveness:  options.Liveness,
	}
}

func (e *Executor) Prompt(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
	attempts := e.retries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		payload, err := e.promptOnce(ctx, role, prompt, schema, attempt, attempts)
		if err == nil {
			return payload, nil
		}
		lastErr = err
		if !errors.Is(err, ErrStuck) || attempt == attempts || ctx.Err() != nil {
			return nil, err
		}
		e.sessions.ResetAfterStuck()
		e.sink.Emit(events.Event{
			Kind: events.KindLog, JobID: role,
			Message: "prompt stuck; retrying",
			Payload: map[string]any{
				"attempt": attempt, "next_attempt": attempt + 1,
				"max_attempts": attempts, "error": err.Error(),
			},
		})
		util.Warn("agents.agent", "prompt stuck; retrying", map[string]any{
			"role": role, "attempt": attempt, "max_attempts": attempts, "error": err.Error(),
		})
	}
	return nil, lastErr
}

func (e *Executor) promptOnce(ctx context.Context, role, prompt string, schema map[string]any, attempt, attempts int) (map[string]any, error) {
	sessionID, cleanup, err := e.sessions.Acquire(ctx, role)
	if err != nil {
		return nil, err
	}
	e.sink.Emit(events.Event{
		Kind: events.KindLLMCallStarted, JobID: role, Status: events.StatusRunning,
		Payload: map[string]any{
			"session_id": sessionID, "attempt": attempt, "max_attempts": attempts,
			"prompt_len": len(prompt), "snapshot_dir": e.directory,
		},
	})
	e.captures.Prompt(role, prompt)
	util.Trace("agents.agent", "prompt start", map[string]any{
		"role": role, "session_id": sessionID, "prompt_len": len(prompt),
	})
	started := time.Now()
	stopWatch, watchDone := e.startLivenessWatch(ctx, role, sessionID)

	var payload map[string]any
	var rawBody []byte
	var textBody string
	if e.verbose != nil {
		payload, rawBody, textBody, err = e.verbose.PromptStructuredVerboseRaw(
			ctx, sessionID, e.directory, prompt, schema,
		)
	} else {
		payload, err = e.client.PromptStructured(ctx, sessionID, e.directory, prompt, schema)
	}

	stopWatch()
	report := <-watchDone
	err = e.relabelIfStuck(err, report, role, sessionID, len(prompt))
	e.captures.Response(role, payload, rawBody, textBody)
	if err != nil {
		return e.handleError(ctx, role, sessionID, prompt, schema, err, started, rawBody, textBody, attempt, attempts, cleanup)
	}
	e.emitSuccess(ctx, role, sessionID, payload, len(prompt), started, attempt, attempts, cleanup)
	return payload, nil
}

func (e *Executor) startLivenessWatch(ctx context.Context, role, sessionID string) (context.CancelFunc, chan *LivenessReport) {
	watchCtx, stopWatch := context.WithCancel(ctx)
	watchDone := make(chan *LivenessReport, 1)
	if e.verbose != nil {
		client, clientOK := e.client.(LivenessClient)
		aborter, aborterOK := e.client.(LivenessAborter)
		if clientOK && aborterOK {
			probe := NewOpenCodeLivenessProbe(client, sessionID, e.directory)
			abort := NewOpenCodeAborter(aborter, sessionID, e.directory)
			go func() {
				watchDone <- RunLiveness(watchCtx, e.liveness, probe, abort, role, e.sink)
			}()
			return stopWatch, watchDone
		}
	}
	watchDone <- nil
	return stopWatch, watchDone
}

func (e *Executor) relabelIfStuck(err error, report *LivenessReport, role, sessionID string, promptLen int) error {
	if report == nil || !report.Aborted {
		return err
	}
	util.Warn("agents.agent", "prompt declared stuck by liveness watchdog", map[string]any{
		"role": role, "session_id": sessionID, "prompt_len": promptLen,
		"reason": report.Reason, "last_tool": report.LastTool, "original_err": errorString(err),
	})
	return NewStuckError(report.Reason)
}

func (e *Executor) handleError(
	ctx context.Context,
	role, sessionID, prompt string,
	schema map[string]any,
	err error,
	started time.Time,
	rawBody []byte,
	textBody string,
	attempt, attempts int,
	cleanup func(),
) (map[string]any, error) {
	if IsNoStructuredPayload(err) {
		fallback, fallbackErr := e.fallbackPromptText(ctx, sessionID, prompt, schema)
		if fallbackErr == nil && fallback != nil {
			e.captures.Response(role, fallback, rawBody, textBody)
			e.sink.Emit(events.Event{
				Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusSuccess,
				Message: "structured slot empty; recovered via free-text fallback",
				Payload: map[string]any{
					"session_id": sessionID, "attempt": attempt, "max_attempts": attempts,
					"duration_ms": time.Since(started).Milliseconds(), "prompt_len": len(prompt),
					"response_keys": MapKeys(fallback), "fallback": "text",
				},
			})
			if cleanup != nil {
				cleanup()
			}
			return fallback, nil
		}
		if fallbackErr != nil {
			err = fmt.Errorf("%w; text fallback also failed: %v", err, fallbackErr)
		}
	}

	e.sessions.Abort(role, sessionID)
	if cleanup != nil {
		cleanup()
	}
	e.sink.Emit(events.Event{
		Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusFailed,
		Message: err.Error(),
		Payload: map[string]any{
			"session_id": sessionID, "attempt": attempt, "max_attempts": attempts,
			"duration_ms": time.Since(started).Milliseconds(),
			"raw_preview": PreviewBytes(rawBody, 360), "text_preview": PreviewString(textBody, 360),
		},
	})
	return nil, fmt.Errorf("%s prompt: %w", role, err)
}

func (e *Executor) emitSuccess(
	ctx context.Context,
	role, sessionID string,
	payload map[string]any,
	promptLen int,
	started time.Time,
	attempt, attempts int,
	cleanup func(),
) {
	tokens := e.recordTokens(ctx, sessionID, role)
	if cleanup != nil {
		cleanup()
	}
	eventPayload := map[string]any{
		"session_id": sessionID, "attempt": attempt, "max_attempts": attempts,
		"duration_ms": time.Since(started).Milliseconds(), "prompt_len": promptLen,
		"response_keys": MapKeys(payload),
	}
	if tokens != nil {
		eventPayload["tokens"] = tokenPayload(tokens)
	}
	e.sink.Emit(events.Event{
		Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusSuccess,
		Payload: eventPayload,
	})
	util.Trace("agents.agent", "prompt ok", map[string]any{"role": role, "session_id": sessionID})
}

func (e *Executor) fallbackPromptText(ctx context.Context, sessionID, prompt string, schema map[string]any) (map[string]any, error) {
	textPrompt := prompt + "\n\nIMPORTANT FORMAT REQUIREMENT:\n" +
		"Reply with a SINGLE JSON object that matches the structure described above.\n" +
		"Do NOT include any explanatory prose before or after the JSON.\n" +
		"Do NOT wrap the JSON in markdown code fences.\n" +
		"If you have nothing to report, reply with: {\"items\": []}\n"
	text, err := e.client.PromptText(ctx, sessionID, e.directory, textPrompt)
	if err != nil {
		return nil, err
	}
	parsed := ScrapeJSONObject(text)
	if parsed == nil {
		return nil, fmt.Errorf("free-text reply did not contain a JSON object (preview=%s)", PreviewString(text, 240))
	}
	_ = schema
	return parsed, nil
}

func (e *Executor) recordTokens(ctx context.Context, sessionID, jobID string) *TokenBucket {
	if e.tokens == nil || sessionID == "" {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	state, err := e.tokens.GetSession(readCtx, sessionID, e.directory)
	if err != nil || state.ID == "" {
		return nil
	}
	return e.totals.Record(jobID, state)
}

func tokenPayload(bucket *TokenBucket) map[string]any {
	return map[string]any{
		"input": bucket.Input, "output": bucket.Output, "reasoning": bucket.Reasoning,
		"cache_read": bucket.CacheRead, "cache_write": bucket.CacheWrite,
		"total": bucket.Total(), "cost": bucket.Cost,
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
