package analyzers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"diffmind/internal/facts"
	"diffmind/internal/snapshot"
)

type llmPackItem struct {
	Evidence facts.Evidence
	Snippet  string
}

func maybeAugmentWithLLM(ctx context.Context, root string, bundle facts.Bundle, opts llmOptions, snapshotID string) (facts.Bundle, int, string, error) {
	if !opts.Enabled {
		return bundle, 0, "", nil
	}
	client, err := newOpenAIClientFromEnv()
	if err != nil {
		return facts.Bundle{}, 0, "", err
	}
	return augmentWithClient(ctx, client, root, bundle, opts, snapshotID)
}

func augmentWithClient(ctx context.Context, client llmClient, root string, bundle facts.Bundle, opts llmOptions, snapshotID string) (facts.Bundle, int, string, error) {
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 20
	}
	if opts.MaxChars <= 0 {
		opts.MaxChars = 50000
	}
	if opts.DefaultConf <= 0 {
		opts.DefaultConf = 0.55
	}
	if opts.Model == "" {
		opts.Model = "gpt-5-mini"
	}
	if opts.Task == "" {
		opts.Task = "augment-routes-http-config"
	}
	if opts.TraceOutputDir == "" {
		opts.TraceOutputDir = filepath.Join(".diffmind", "llm", "traces")
	}

	pack, filesCount, err := buildEvidencePack(root, snapshotID, opts.MaxFiles, opts.MaxChars)
	if err != nil {
		return facts.Bundle{}, 0, "", err
	}
	if len(pack) == 0 {
		return bundle, 0, "", nil
	}

	systemPrompt := "You are a static-analysis augmentation agent. Return JSON only. Never invent evidence IDs."
	userPrompt := buildLLMPrompt(opts.Task, bundle, pack)

	raw, err := client.CompleteJSON(ctx, opts.Model, systemPrompt, userPrompt)
	if err != nil {
		return facts.Bundle{}, 0, "", err
	}

	parsed, err := parseLLMResponse(raw)
	if err != nil {
		tracePath, _ := writeTrace(opts.TraceOutputDir, llmTrace{
			TimestampUTC:      time.Now().UTC(),
			Model:             opts.Model,
			Task:              opts.Task,
			MaxFiles:          opts.MaxFiles,
			MaxChars:          opts.MaxChars,
			EvidenceCount:     len(pack),
			EvidenceFileCount: filesCount,
			Prompt:            userPrompt,
			RawResponse:       raw,
		})
		return facts.Bundle{}, 0, tracePath, fmt.Errorf("parse llm response: %w", err)
	}

	evidenceByID := map[string]facts.Evidence{}
	for _, ev := range bundle.Evidence {
		evidenceByID[ev.ID] = ev
	}
	packByID := map[string]facts.Evidence{}
	for _, item := range pack {
		packByID[item.Evidence.ID] = item.Evidence
	}

	factIDs := map[string]struct{}{}
	for _, f := range bundle.Facts {
		factIDs[f.ID] = struct{}{}
	}

	added := 0
	augmented := bundle
	for _, pf := range parsed.Facts {
		if strings.TrimSpace(pf.Type) == "" {
			continue
		}
		validIDs := make([]string, 0, len(pf.EvidenceIDs))
		for _, id := range pf.EvidenceIDs {
			ev, ok := packByID[id]
			if !ok {
				continue
			}
			evidenceByID[id] = ev
			validIDs = append(validIDs, id)
		}
		if len(validIDs) == 0 {
			continue
		}
		conf := pf.Confidence
		if conf <= 0 || conf > 1 {
			conf = opts.DefaultConf
		}
		f := facts.NewFact(pf.Type, pf.Attributes, validIDs, conf, facts.Provenance{
			AnalyzerID:      "llm.augment.v1",
			AnalyzerVersion: opts.Model,
			Deterministic:   false,
			Inferred:        true,
		})
		if _, exists := factIDs[f.ID]; exists {
			continue
		}
		factIDs[f.ID] = struct{}{}
		augmented.Facts = append(augmented.Facts, f)
		added++
	}

	augmented.Evidence = make([]facts.Evidence, 0, len(evidenceByID))
	for _, ev := range evidenceByID {
		augmented.Evidence = append(augmented.Evidence, ev)
	}
	sort.Slice(augmented.Evidence, func(i, j int) bool { return augmented.Evidence[i].ID < augmented.Evidence[j].ID })
	sort.Slice(augmented.Facts, func(i, j int) bool { return augmented.Facts[i].ID < augmented.Facts[j].ID })
	if err := facts.ValidateBundle(augmented); err != nil {
		return facts.Bundle{}, 0, "", fmt.Errorf("validate augmented bundle: %w", err)
	}

	tracePath, err := writeTrace(opts.TraceOutputDir, llmTrace{
		TimestampUTC:      time.Now().UTC(),
		Model:             opts.Model,
		Task:              opts.Task,
		MaxFiles:          opts.MaxFiles,
		MaxChars:          opts.MaxChars,
		EvidenceCount:     len(pack),
		EvidenceFileCount: filesCount,
		Prompt:            userPrompt,
		RawResponse:       raw,
		ParsedFacts:       parsed.Facts,
	})
	if err != nil {
		return facts.Bundle{}, 0, "", err
	}
	return augmented, added, tracePath, nil
}

func buildEvidencePack(root string, snapshotID string, maxFiles int, maxChars int) ([]llmPackItem, int, error) {
	inv, err := snapshot.BuildInventory(root, snapshot.InventoryOptions{ExcludeDirs: map[string]struct{}{
		".git": {}, ".diffmind": {}, ".gocache": {}, "bin": {}, "node_modules": {},
	}})
	if err != nil {
		return nil, 0, err
	}

	keywords := []string{"route", "router", "endpoint", "request", "fetch", "axios", "getenv", "process.env", "@value", "viper", "http.", "openapi", "terraform", "k8s", "workflow", "ci", "service", "mapping"}
	items := make([]llmPackItem, 0)
	seen := map[string]struct{}{}
	filesUsed := map[string]struct{}{}
	charCount := 0

	for _, entry := range inv {
		if len(filesUsed) >= maxFiles || charCount >= maxChars {
			break
		}
		if entry.FileType == "binary" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Path))
		if !isLLMCandidateFile(entry.Path, ext) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(entry.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		addedInFile := false
		for i, line := range lines {
			if len(filesUsed) >= maxFiles || charCount >= maxChars {
				break
			}
			lower := strings.ToLower(line)
			if !containsAny(lower, keywords) {
				continue
			}
			snippet := strings.TrimSpace(line)
			if snippet == "" {
				continue
			}
			if charCount+len(snippet) > maxChars {
				break
			}
			ev := facts.NewEvidence(snapshotID, entry.Path, i+1, 1, i+1, max(1, len(line)), snippet)
			if _, exists := seen[ev.ID]; exists {
				continue
			}
			seen[ev.ID] = struct{}{}
			items = append(items, llmPackItem{Evidence: ev, Snippet: snippet})
			charCount += len(snippet)
			addedInFile = true
		}
		if addedInFile {
			filesUsed[entry.Path] = struct{}{}
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Evidence.ID < items[j].Evidence.ID })
	return items, len(filesUsed), nil
}

func isLLMCandidateFile(path string, ext string) bool {
	if strings.HasPrefix(path, ".github/workflows/") {
		return true
	}
	switch ext {
	case ".go", ".js", ".ts", ".tsx", ".java", ".py", ".yaml", ".yml", ".json", ".tf", ".proto":
		return true
	default:
		return strings.EqualFold(filepath.Base(path), "dockerfile")
	}
}

func containsAny(line string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(line, k) {
			return true
		}
	}
	return false
}

func buildLLMPrompt(task string, deterministic facts.Bundle, pack []llmPackItem) string {
	var b strings.Builder
	b.WriteString("Task: ")
	b.WriteString(task)
	b.WriteString("\n")
	b.WriteString("You receive deterministic findings and evidence snippets. Return only JSON with shape:\n")
	b.WriteString(`{"facts":[{"type":"Endpoint|ExternalCall|ConfigKey|RuntimeUnit|PipelineStep|InfraResource","attributes":{},"evidence_ids":["..."],"confidence":0.0}]}`)
	b.WriteString("\nRules:\n")
	b.WriteString("- evidence_ids must come only from provided IDs.\n")
	b.WriteString("- do not create facts without evidence_ids.\n")
	b.WriteString("- keep confidence in [0,1].\n")
	b.WriteString("- return concise attributes; do not hallucinate file paths.\n")
	b.WriteString("Deterministic summary:\n")
	b.WriteString(fmt.Sprintf("facts=%d evidence=%d\n", len(deterministic.Facts), len(deterministic.Evidence)))
	b.WriteString("Evidence pack:\n")
	for _, item := range pack {
		ev := item.Evidence
		b.WriteString(fmt.Sprintf("- id=%s file=%s span=%d:%d-%d:%d snippet=%q\n", ev.ID, ev.FilePath, ev.StartLine, ev.StartCol, ev.EndLine, ev.EndCol, item.Snippet))
	}
	return b.String()
}

func parseLLMResponse(raw string) (llmResponsePayload, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var out llmResponsePayload
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return llmResponsePayload{}, err
	}
	if out.Facts == nil {
		out.Facts = []llmFactOutput{}
	}
	return out, nil
}

func writeTrace(dir string, trace llmTrace) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create llm trace dir: %w", err)
	}
	name := trace.TimestampUTC.Format("20060102T150405.000000000Z07") + ".json"
	name = strings.ReplaceAll(name, ":", "-")
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal llm trace: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write llm trace: %w", err)
	}
	return path, nil
}
