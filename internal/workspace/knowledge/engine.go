package knowledge

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge/extractors"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// Engine executes packs against repositories.
type Engine struct {
	log *util.Logger
}

// NewEngine creates a new pack execution engine.
func NewEngine(log *util.Logger) *Engine {
	return &Engine{log: log}
}

// Run executes all matching packs against a single repository
// and returns the extraction results.
func (e *Engine) Run(bp *Pack, repoPath string) []ExtractionResult {
	var results []ExtractionResult

	for _, ext := range bp.Extractions {
		strategy := ext.Strategy
		if strategy == "" {
			strategy = "field_path"
		}

		// Resolve source files.
		files, err := ResolveGlob(repoPath, ext.Source.Glob)
		if err != nil {
			e.log.Debug("glob failed", "pack", bp.Name, "glob", ext.Source.Glob, "error", err.Error())
			continue
		}
		if len(files) == 0 {
			e.log.Debug("no files matched", "pack", bp.Name, "glob", ext.Source.Glob)
			continue
		}
		files = filterIgnored(repoPath, files, bp.Ignore)

		switch strategy {
		case "field_path":
			results = append(results, e.runFieldPath(bp, repoPath, ext, files)...)
		case "regex":
			results = append(results, e.runRegex(bp, repoPath, ext, files)...)
		default:
			e.log.Warn("unknown extraction strategy", "strategy", strategy)
		}
	}

	return results
}

func (e *Engine) runFieldPath(pack *Pack, repoPath string, ext Extraction, files []string) []ExtractionResult {
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
			source, _ := filepath.Rel(repoPath, f)
			results = append(results, ExtractionResult{
				PackID:         pack.ID,
				PackVersion:    pack.Version,
				PackPriority:   pack.Priority,
				ExtractionName: ext.Name,
				SourceFile:     filepath.ToSlash(source),
				Values:         values,
			})
		}
	}
	return results
}

func (e *Engine) runRegex(pack *Pack, repoPath string, ext Extraction, files []string) []ExtractionResult {
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
			source, _ := filepath.Rel(repoPath, f)
			results = append(results, ExtractionResult{
				PackID:         pack.ID,
				PackVersion:    pack.Version,
				PackPriority:   pack.Priority,
				ExtractionName: ext.Name,
				SourceFile:     filepath.ToSlash(source),
				Values:         values,
			})
		}
	}
	return results
}

func filterIgnored(repoPath string, files, patterns []string) []string {
	if len(patterns) == 0 {
		return files
	}
	out := files[:0]
	for _, file := range files {
		rel, err := filepath.Rel(repoPath, file)
		if err != nil {
			continue
		}
		ignored := false
		for _, pattern := range patterns {
			if matchesGlob(filepath.ToSlash(rel), pattern) {
				ignored = true
				break
			}
		}
		if !ignored {
			out = append(out, file)
		}
	}
	return out
}

func matchesGlob(path, pattern string) bool {
	path = filepath.ToSlash(path)
	pattern = filepath.ToSlash(pattern)
	var expression strings.Builder
	expression.WriteString("^")
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					expression.WriteString("(?:.*/)?")
					i += 3
				} else {
					expression.WriteString(".*")
					i += 2
				}
			} else {
				expression.WriteString("[^/]*")
				i++
			}
		case '?':
			expression.WriteString("[^/]")
			i++
		default:
			expression.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			i++
		}
	}
	expression.WriteString("$")
	matched, err := regexp.MatchString(expression.String(), path)
	return err == nil && matched
}
