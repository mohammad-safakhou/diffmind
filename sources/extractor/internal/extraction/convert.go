package extraction

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// toBase converts an llmEntity into a model.BaseEntity, returning either the
// populated entity or an UnresolvedItem describing why the candidate was
// dropped. It is the single gate where confidence + source-location rules are
// enforced on converted results.
func ToBase(repoPath string, obj objectives.Objective, e Candidate, minConfidence float64) (model.BaseEntity, *model.UnresolvedItem) {
	name := strings.TrimSpace(e.Name)
	typ, typeOK := CanonicalObjectiveType(obj, e.Type)
	if name == "" || typ == "" {
		return model.BaseEntity{}, &model.UnresolvedItem{
			Kind: obj.Kind, Type: typ, Name: name,
			ReasonCode: "invalid_entity", Reason: "Missing name/type",
			Confidence: e.Confidence,
			Evidence:   ToEvidence(e.Evidence),
		}
	}
	if !typeOK {
		return model.BaseEntity{}, &model.UnresolvedItem{
			Kind:       obj.Kind,
			Type:       NormalizeType(e.Type),
			Name:       name,
			ReasonCode: "wrong_objective_type",
			Reason:     fmt.Sprintf("Type %q does not match objective type %q", e.Type, obj.Type),
			Confidence: e.Confidence,
			Evidence:   ToEvidence(e.Evidence),
		}
	}
	if e.Confidence < minConfidence {
		return model.BaseEntity{}, &model.UnresolvedItem{
			Kind: obj.Kind, Type: typ, Name: name,
			ReasonCode: "low_confidence",
			Reason:     fmt.Sprintf("Confidence %.2f below threshold %.2f", e.Confidence, minConfidence),
			Confidence: e.Confidence,
			Evidence:   ToEvidence(e.Evidence),
		}
	}
	locations := ToLocations(e.Locations)
	if len(locations) == 0 {
		return model.BaseEntity{}, &model.UnresolvedItem{
			Kind: obj.Kind, Type: typ, Name: name,
			ReasonCode: "no_source_location",
			Reason:     "No source location provided",
			Confidence: e.Confidence,
			Evidence:   ToEvidence(e.Evidence),
		}
	}
	evidence := ToEvidence(e.Evidence)
	if len(evidence) == 0 {
		evidence = append(evidence, model.Evidence{Location: locations[0], Snippet: e.Summary, Source: "opencode"})
	}
	inputs := make([]model.InputSpec, 0, len(e.Inputs))
	for _, in := range e.Inputs {
		if strings.TrimSpace(in.Name) != "" {
			inputs = append(inputs, model.InputSpec{
				Name:        in.Name,
				Type:        in.Type,
				Required:    in.Required,
				Description: in.Description,
			})
		}
	}
	id := util.StableID(string(obj.Kind), typ, name, locations[0].File, fmt.Sprintf("%d:%d", locations[0].StartLine, locations[0].EndLine))
	base := model.BaseEntity{
		ID:           id,
		Type:         typ,
		Name:         name,
		Service:      repoPath,
		Inputs:       inputs,
		Summary:      DefaultStr(e.Summary, "Extracted by OpenCode"),
		KeyActions:   e.Actions,
		Locations:    locations,
		Evidence:     evidence,
		Confidence:   e.Confidence,
		Tags:         e.Tags,
		Details:      e.Details,
		PluginSource: "opencode",
	}
	EnrichEntityGrouping(&base)
	return base, nil
}

func ToLocations(in []Location) []model.Location {
	out := make([]model.Location, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v.File) == "" || v.StartLine <= 0 {
			continue
		}
		end := v.EndLine
		if end < v.StartLine {
			end = v.StartLine
		}
		out = append(out, model.Location{File: v.File, StartLine: v.StartLine, EndLine: end})
	}
	return out
}

func ToEvidence(in []Evidence) []model.Evidence {
	out := make([]model.Evidence, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v.File) == "" || v.StartLine <= 0 {
			continue
		}
		end := v.EndLine
		if end < v.StartLine {
			end = v.StartLine
		}
		source := v.Source
		if strings.TrimSpace(source) == "" {
			source = "opencode"
		}
		out = append(out, model.Evidence{
			Location: model.Location{File: v.File, StartLine: v.StartLine, EndLine: end},
			Snippet:  v.Snippet,
			Source:   source,
		})
	}
	return out
}

// toConnectionPaths was the LLM→model.ConnectionPath converter used by
// the old connections stage. The SCIP-driven stage builds
// model.ConnectionPath directly from scip.Path, so this helper is
// obsolete and has been removed. See internal/stage/connections/path.go's
// convertASTPath function for the replacement.

func FillCondition(c model.Condition, fallbackExplanation string) model.Condition {
	if strings.TrimSpace(c.Kind) == "" {
		c.Kind = "predicate"
	}
	if strings.TrimSpace(c.Expression) == "" {
		c.Expression = "true"
	}
	if strings.TrimSpace(c.Explanation) == "" {
		c.Explanation = DefaultStr(fallbackExplanation, "always")
	}
	return c
}

func DefaultStr(in, fallback string) string {
	if strings.TrimSpace(in) == "" {
		return fallback
	}
	return in
}

// errString returns err.Error() or "" when err is nil. Used in event
// payloads where we want a stable string field even on nil error.
func ErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// safeJobID converts an arbitrary entity name into a slug suitable for use
// inside an event JobID. We keep alnum, dashes, dots, slashes, and colons;
// everything else collapses to a dash. The truncation keeps log lines and
// graph node ids reasonably short.
func SafeJobID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.' || r == '/' || r == ':':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}

func DedupeUnresolved(in []model.UnresolvedItem) []model.UnresolvedItem {
	seen := map[string]struct{}{}
	out := make([]model.UnresolvedItem, 0, len(in))
	for _, u := range in {
		key := string(u.Kind) + "|" + u.Type + "|" + u.Name + "|" + u.ReasonCode
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, u)
	}
	return out
}

func DedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func ParseEntities(v any) []Candidate {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []Candidate
	_ = json.Unmarshal(b, &out)
	return out
}

func ParseSingleEntity(v any) *Candidate {
	if v == nil {
		return nil
	}
	// Tolerate either "item": <obj> or "item": [<obj>, ...].
	if list, ok := v.([]any); ok {
		items := ParseEntities(list)
		if len(items) == 0 {
			return nil
		}
		return &items[0]
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var e Candidate
	if err := json.Unmarshal(b, &e); err != nil {
		return nil
	}
	if strings.TrimSpace(e.Name) == "" && strings.TrimSpace(e.Type) == "" {
		return nil
	}
	return &e
}

// parseConnections was the JSON → []llmConnection decoder used by the
// old LLM-based connections stage. With the deterministic SCIP path
// no LLM connection JSON is ever produced, so this helper is removed.

func ParseRepoFacts(v map[string]any) *RepoFacts {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var facts RepoFacts
	if err := json.Unmarshal(b, &facts); err != nil {
		return nil
	}
	return &facts
}
