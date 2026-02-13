package consolidation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"diffmind/internal/facts"
)

func consolidate(bundle facts.Bundle, forcedSnapshotID string) (IntelligenceBundle, Report, error) {
	snapshotID := forcedSnapshotID
	if snapshotID == "" {
		snapshotID = detectSnapshotID(bundle)
	}
	if snapshotID == "" {
		snapshotID = "unknown"
	}

	evidenceByID := make(map[string]facts.Evidence, len(bundle.Evidence))
	for _, ev := range bundle.Evidence {
		evidenceByID[ev.ID] = ev
	}

	byKey := map[string]*Entity{}
	duplicates := 0

	for _, f := range bundle.Facts {
		naturalKey := buildNaturalKey(f)
		key := f.Type + "|" + naturalKey
		entity, exists := byKey[key]
		if !exists {
			entity = &Entity{
				ID:          stableEntityID(snapshotID, f.Type, naturalKey),
				Type:        f.Type,
				NaturalKey:  naturalKey,
				Attributes:  cloneAttributes(f.Attributes),
				EvidenceIDs: uniqueSorted(f.EvidenceIDs),
				FactIDs:     []string{f.ID},
				Confidence:  f.Confidence,
			}
			attachRuntimeReference(entity, byKey, evidenceByID)
			byKey[key] = entity
			continue
		}
		duplicates++
		entity.EvidenceIDs = uniqueSorted(append(entity.EvidenceIDs, f.EvidenceIDs...))
		entity.FactIDs = uniqueSorted(append(entity.FactIDs, f.ID))
		if f.Confidence > entity.Confidence {
			entity.Confidence = f.Confidence
		}
		mergeAttributes(entity.Attributes, f.Attributes)
		attachRuntimeReference(entity, byKey, evidenceByID)
	}

	entities := make([]Entity, 0, len(byKey))
	report := Report{GeneratedAt: time.Now().UTC(), SnapshotID: snapshotID, InputFacts: len(bundle.Facts), DuplicatesMerged: duplicates}
	for _, e := range byKey {
		entities = append(entities, *e)
		switch e.Type {
		case "RuntimeUnit":
			report.RuntimeUnits++
		case "Endpoint":
			report.Endpoints++
		case "ExternalCall":
			report.ExternalCalls++
		case "ConfigKey":
			report.ConfigKeys++
		case "PipelineStep":
			report.PipelineSteps++
		case "InfraResource":
			report.InfraResources++
		}
	}
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Type == entities[j].Type {
			return entities[i].NaturalKey < entities[j].NaturalKey
		}
		return entities[i].Type < entities[j].Type
	})
	report.OutputEntities = len(entities)

	return IntelligenceBundle{
		SnapshotID:  snapshotID,
		GeneratedAt: time.Now().UTC(),
		Entities:    entities,
	}, report, nil
}

func detectSnapshotID(bundle facts.Bundle) string {
	if len(bundle.Evidence) > 0 {
		return bundle.Evidence[0].SnapshotID
	}
	return ""
}

func buildNaturalKey(f facts.Fact) string {
	a := f.Attributes
	switch f.Type {
	case "RuntimeUnit":
		return joinKey(values(a, "language", "kind", "file", "script", "command", "value", "port"))
	case "Endpoint":
		return joinKey(values(a, "direction", "method", "path", "framework", "runtime_unit_id"))
	case "ConfigKey":
		return joinKey(values(a, "key", "pattern", "runtime_unit_id"))
	case "ExternalCall":
		return joinKey(values(a, "protocol", "method", "target", "library", "runtime_unit_id"))
	case "PipelineStep":
		return joinKey(values(a, "provider", "kind", "value"))
	case "InfraResource":
		return joinKey(values(a, "provider", "kind", "resource_type", "name", "file"))
	default:
		b, _ := json.Marshal(a)
		return string(b)
	}
}

func attachRuntimeReference(entity *Entity, entities map[string]*Entity, evidenceByID map[string]facts.Evidence) {
	if entity.Type == "RuntimeUnit" {
		return
	}
	if _, ok := entity.Attributes["runtime_unit_id"]; ok {
		return
	}
	for _, evID := range entity.EvidenceIDs {
		ev, ok := evidenceByID[evID]
		if !ok {
			continue
		}
		for _, ru := range entities {
			if ru.Type != "RuntimeUnit" {
				continue
			}
			if file, ok := ru.Attributes["file"].(string); ok && file == ev.FilePath {
				entity.Attributes["runtime_unit_id"] = ru.ID
				return
			}
		}
	}
}

func values(attrs map[string]any, keys ...string) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if v, ok := attrs[key]; ok {
			out = append(out, fmt.Sprint(v))
		} else {
			out = append(out, "")
		}
	}
	return out
}

func joinKey(parts []string) string {
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "|")
}

func mergeAttributes(dst map[string]any, src map[string]any) {
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
}

func cloneAttributes(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func stableEntityID(snapshotID string, entityType string, naturalKey string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(snapshotID))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(entityType))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(naturalKey))
	return hex.EncodeToString(h.Sum(nil))
}
