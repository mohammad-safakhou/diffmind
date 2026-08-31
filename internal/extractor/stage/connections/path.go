package connections

import (
	"fmt"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

// buildASTConnection assembles a model.Connection from tree-sitter paths.
func buildASTConnection(
	exposure model.Exposure,
	dep model.Dependency,
	paths []astpkg.CallPath,
	minConfidence float64,
) model.Connection {
	mPaths := make([]model.ConnectionPath, 0, len(paths))
	var primaryCond model.Condition
	var locs []model.Location

	for i, p := range paths {
		mp := convertASTPath(p, dep.Type)
		if mp.ID == "" {
			mp.ID = fmt.Sprintf("path-%d", i+1)
		}
		mPaths = append(mPaths, mp)
		if mp.Condition.Kind != "" && primaryCond.Kind == "" {
			primaryCond = mp.Condition
		}
		for _, step := range mp.Steps {
			if step.Location.File != "" {
				locs = append(locs, step.Location)
			}
		}
	}

	if len(locs) == 0 {
		locs = append(locs, exposure.Locations...)
	}
	if len(locs) == 0 {
		locs = append(locs, dep.Locations...)
	}

	pathSig := buildASTPathSignature(paths)
	connID := util.StableID(exposure.ID, dep.ID, pathSig)
	confidence := scoreASTConfidence(paths, minConfidence)

	return model.Connection{
		ID:             connID,
		FromExposureID: exposure.ID,
		ToDependencyID: dep.ID,
		Source:         model.ConnectionSourceAST,
		Condition:      primaryCond,
		PathSignature:  pathSig,
		Summary:        fmt.Sprintf("%s → %s", exposure.Name, dep.Name),
		Locations:      dedupeLocations(locs),
		Evidence:       buildASTEvidence(paths),
		Confidence:     confidence,
		FromType:       exposure.Type,
		ToType:         dep.Type,
		Paths:          mPaths,
	}
}

// convertASTPath converts a tree-sitter CallPath to a model.ConnectionPath.
func convertASTPath(p astpkg.CallPath, depType string) model.ConnectionPath {
	steps := make([]model.ConnectionPathStep, 0, len(p.Steps))
	var pathCond model.Condition

	for _, s := range p.Steps {
		loc := model.Location{
			File:      s.File,
			StartLine: int(s.Range.StartLine) + 1, // convert 0-based to 1-based
			EndLine:   int(s.Range.EndLine) + 1,
		}

		// Convert per-step condition from tree-sitter.
		stepCond := model.Condition{
			Kind:        s.Condition.Kind,
			Expression:  s.Condition.Expression,
			Explanation: s.Condition.Explanation,
		}
		// If this step has a meaningful condition and the path has none, promote it.
		if stepCond.Kind != "" && stepCond.Kind != "unconditional" && pathCond.Kind == "" {
			pathCond = stepCond
		}

		// Convert per-step repetition — encode in condition when loop.
		if s.Repetition.Kind == "loop" {
			if stepCond.Kind == "" || stepCond.Kind == "unconditional" {
				stepCond = model.Condition{
					Kind:        "loop",
					Expression:  s.Repetition.IteratesOver,
					Explanation: "Call is inside a loop; executed once per element",
				}
				if pathCond.Kind == "" {
					pathCond = stepCond
				}
			}
		}

		// Build args summary.
		argsSummary := ""
		if len(s.Arguments) > 0 {
			parts := make([]string, 0, len(s.Arguments))
			for _, a := range s.Arguments {
				parts = append(parts, a.Source)
			}
			argsSummary = strings.Join(parts, ", ")
		}

		steps = append(steps, model.ConnectionPathStep{
			Order:     s.Order,
			Action:    "invoke",
			Operation: depType,
			From:      s.Caller,
			To:        s.Callee,
			Condition: stepCond,
			Location:  loc,
		})
		_ = argsSummary // will be exposed in graph export endpoint
	}

	// Roll up path condition.
	if pathCond.Kind == "" {
		pathCond = model.Condition{Kind: "unconditional"}
	}

	return model.ConnectionPath{
		ID:        buildASTPathID(p),
		Summary:   buildASTPathSummary(p),
		Condition: pathCond,
		Steps:     steps,
	}
}

func buildASTPathID(p astpkg.CallPath) string {
	parts := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		parts = append(parts, s.Callee)
	}
	return util.StableID(strings.Join(parts, "->"))
}

func buildASTPathSummary(p astpkg.CallPath) string {
	if len(p.Steps) == 0 {
		return ""
	}
	first := lastIdent(p.Steps[0].Caller)
	last := lastIdent(p.Steps[len(p.Steps)-1].Callee)
	return fmt.Sprintf("%s → %s (%d hops)", first, last, len(p.Steps))
}

func buildASTPathSignature(paths []astpkg.CallPath) string {
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		parts = append(parts, buildASTPathID(p))
	}
	sort.Strings(parts)
	return strings.Join(parts, "||")
}

func buildASTEvidence(paths []astpkg.CallPath) []model.Evidence {
	seen := map[string]struct{}{}
	var out []model.Evidence
	for _, p := range paths {
		for _, s := range p.Steps {
			key := s.File + ":" + fmt.Sprint(s.Range.StartLine)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model.Evidence{
				Location: model.Location{
					File:      s.File,
					StartLine: int(s.Range.StartLine) + 1,
					EndLine:   int(s.Range.EndLine) + 1,
				},
				Source: "ast",
			})
			if len(out) >= 5 {
				return out
			}
		}
	}
	return out
}

func scoreASTConfidence(paths []astpkg.CallPath, minConfidence float64) float64 {
	if len(paths) == 0 {
		return minConfidence
	}
	// Shorter paths = higher confidence. Zero hops (the dependency's call site
	// is inside the entry method itself) is the strongest evidence of all.
	minHops := len(paths[0].Steps)
	for _, p := range paths {
		if len(p.Steps) < minHops {
			minHops = len(p.Steps)
		}
	}
	score := 0.98 - float64(minHops-1)*0.04
	if score > 0.99 {
		score = 0.99
	}
	if score < 0.5 {
		score = 0.5
	}
	if score < minConfidence {
		score = minConfidence
	}
	return score
}
