package blueprints

import (
	"github.com/mohammad-safakhou/diffmind/internal/blueprints/extractors"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Engine executes blueprints against repositories.
type Engine struct {
	client *opencode.Client
	log    *util.Logger
}

// NewEngine creates a new blueprint execution engine.
func NewEngine(client *opencode.Client, log *util.Logger) *Engine {
	return &Engine{client: client, log: log}
}

// Run executes all matching blueprints against a single repository
// and returns the extraction results.
func (e *Engine) Run(bp *Blueprint, repoPath, sessionID string) []ExtractionResult {
	var results []ExtractionResult

	for _, ext := range bp.Extractions {
		strategy := ext.Strategy
		if strategy == "" {
			strategy = "field_path"
		}

		// Resolve source files.
		files, err := ResolveGlob(repoPath, ext.Source.Glob)
		if err != nil {
			e.log.Debug("glob failed", "blueprint", bp.Name, "glob", ext.Source.Glob, "error", err.Error())
			continue
		}
		if len(files) == 0 {
			e.log.Debug("no files matched", "blueprint", bp.Name, "glob", ext.Source.Glob)
			continue
		}

		switch strategy {
		case "field_path":
			results = append(results, e.runFieldPath(bp.Name, ext, files)...)
		case "regex":
			results = append(results, e.runRegex(bp.Name, ext, files)...)
		case "llm":
			results = append(results, e.runLLM(bp.Name, ext, files, sessionID)...)
		default:
			e.log.Warn("unknown extraction strategy", "strategy", strategy)
		}
	}

	return results
}

func (e *Engine) runFieldPath(bpName string, ext Extraction, files []string) []ExtractionResult {
	var results []ExtractionResult
	for _, f := range files {
		values := make(map[string]any)
		for _, ef := range ext.Extract {
			if ef.Field == "" {
				continue
			}
			val, err := extractors.ExtractFieldPath(f, ef.Field)
			if err != nil {
				e.log.Debug("field extraction failed", "file", f, "field", ef.Field, "error", err.Error())
				continue
			}
			values[ef.MapsTo] = val
		}
		if len(values) > 0 {
			results = append(results, ExtractionResult{
				BlueprintName:  bpName,
				ExtractionName: ext.Name,
				SourceFile:     f,
				Values:         values,
			})
		}
	}
	return results
}

func (e *Engine) runRegex(bpName string, ext Extraction, files []string) []ExtractionResult {
	var results []ExtractionResult
	for _, f := range files {
		values := make(map[string]any)
		for _, ef := range ext.Extract {
			if ef.Pattern == "" {
				continue
			}
			matches, err := extractors.ExtractRegex(f, ef.Pattern)
			if err != nil {
				e.log.Debug("regex extraction failed", "file", f, "pattern", ef.Pattern, "error", err.Error())
				continue
			}
			if len(matches) == 1 {
				values[ef.MapsTo] = matches[0]
			} else if len(matches) > 1 {
				values[ef.MapsTo] = matches
			}
		}
		if len(values) > 0 {
			results = append(results, ExtractionResult{
				BlueprintName:  bpName,
				ExtractionName: ext.Name,
				SourceFile:     f,
				Values:         values,
			})
		}
	}
	return results
}

func (e *Engine) runLLM(bpName string, ext Extraction, files []string, sessionID string) []ExtractionResult {
	if e.client == nil || sessionID == "" {
		e.log.Warn("LLM extraction skipped: no client or session")
		return nil
	}

	var fieldNames []string
	for _, ef := range ext.Extract {
		fieldNames = append(fieldNames, ef.MapsTo)
	}

	values, err := extractors.ExtractWithLLM(e.client, sessionID, files, ext.PromptHint, fieldNames)
	if err != nil {
		e.log.Error("LLM extraction failed", "blueprint", bpName, "error", err.Error())
		return nil
	}

	return []ExtractionResult{{
		BlueprintName:  bpName,
		ExtractionName: ext.Name,
		SourceFile:     files[0], // representative
		Values:         values,
	}}
}
