package consolidation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
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
	factsByKey := map[string][]facts.Fact{}
	duplicates := 0
	inputFacts := len(bundle.Facts)

	for i, f := range bundle.Facts {
		if inputFacts >= 100000 && (i+1)%200000 == 0 {
			slog.Info("consolidation progress",
				"phase", "entity_merge",
				"facts_processed", i+1,
				"input_facts", inputFacts,
				"distinct_entities", len(byKey),
			)
		}
		naturalKey := buildNaturalKey(f)
		key := f.Type + "|" + naturalKey
		factsByKey[key] = append(factsByKey[key], f)
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
			entity.Attributes["confidence_band"] = confidenceBand(entity.Confidence)
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
		entity.Attributes["confidence_band"] = confidenceBand(entity.Confidence)
	}

	conflictEntities := buildConflictEntities(snapshotID, factsByKey)
	for _, ce := range conflictEntities {
		key := ce.Type + "|" + ce.NaturalKey
		byKey[key] = ce
	}

	entities := make([]Entity, 0, len(byKey))
	report := Report{GeneratedAt: time.Now().UTC(), SnapshotID: snapshotID, InputFacts: inputFacts, DuplicatesMerged: duplicates}
	for _, e := range byKey {
		entities = append(entities, *e)
		switch confidenceBand(e.Confidence) {
		case "high":
			report.ConfidenceHigh++
		case "medium":
			report.ConfidenceMedium++
		default:
			report.ConfidenceLow++
		}
		switch e.Type {
		case "RuntimeUnit":
			report.RuntimeUnits++
		case "Endpoint":
			report.Endpoints++
		case "ExternalCall":
			report.ExternalCalls++
		case "ConfigKey":
			report.ConfigKeys++
		case "SensitiveSurface":
			report.SensitiveSurfaces++
		case "PipelineStep":
			report.PipelineSteps++
		case "InfraResource":
			report.InfraResources++
		case "BuildArtifact":
			report.BuildArtifacts++
		case "Deployment":
			report.Deployments++
		case "Dependency":
			report.Dependencies++
		case "OwnershipRule":
			report.OwnershipRules++
		case "DependencyRisk":
			report.DependencyRisks++
		case "Conflict":
			report.Conflicts++
		case "CodeSymbol":
			report.CodeSymbols++
		case "CodeCall":
			report.CodeCalls++
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
		return joinKey(values(a, "key", "pattern", "runtime_unit_id", "environment", "source_kind", "sensitive"))
	case "SensitiveSurface":
		return joinKey(values(a, "kind", "key", "reference", "classification", "environment", "source_kind"))
	case "ExternalCall":
		return joinKey(values(a, "protocol", "method", "target", "library", "runtime_unit_id"))
	case "PipelineStep":
		return joinKey(values(a, "provider", "kind", "value"))
	case "InfraResource":
		return joinKey(values(a, "provider", "kind", "resource_type", "name", "file"))
	case "BuildArtifact":
		return joinKey(values(a, "artifact_type", "name", "service", "build_command", "provider", "produced_by", "file"))
	case "Deployment":
		return joinKey(values(a, "platform", "resource_kind", "name", "image", "file"))
	case "CodeSymbol":
		return joinKey(values(a, "language", "symbol_kind", "name", "file", "line", "col"))
	case "CodeCall":
		return joinKey(values(a, "language", "callee", "file", "line", "col"))
	case "Dependency":
		return joinKey(values(a, "ecosystem", "name", "version", "scope", "internal", "source_file"))
	case "OwnershipRule":
		return joinKey(values(a, "pattern", "owner", "source_file"))
	case "DependencyRisk":
		return joinKey(values(a, "ecosystem", "name", "version", "risk_type", "severity", "source_file"))
	case "Conflict":
		return joinKey(values(a, "entity_type", "entity_natural_key", "status"))
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

func confidenceBand(v float64) string {
	if v >= 0.9 {
		return "high"
	}
	if v >= 0.7 {
		return "medium"
	}
	return "low"
}

func buildConflictEntities(snapshotID string, factsByKey map[string][]facts.Fact) []*Entity {
	out := make([]*Entity, 0)
	processed := 0
	for key, factsForKey := range factsByKey {
		processed++
		if len(factsByKey) >= 100000 && processed%200000 == 0 {
			slog.Info("consolidation progress",
				"phase", "conflict_detection",
				"keys_processed", processed,
				"total_keys", len(factsByKey),
				"conflicts_found", len(out),
			)
		}
		if len(factsForKey) < 2 {
			continue
		}
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		entityType := parts[0]
		naturalKey := parts[1]
		conflictKeys, observedValues := detectAttributeConflicts(factsForKey)
		if len(conflictKeys) == 0 {
			continue
		}

		evIDs := make([]string, 0)
		factIDs := make([]string, 0, len(factsForKey))
		for _, f := range factsForKey {
			evIDs = append(evIDs, f.EvidenceIDs...)
			factIDs = append(factIDs, f.ID)
		}
		sort.Strings(conflictKeys)
		conflictNaturalKey := joinKey([]string{entityType, naturalKey, strings.Join(conflictKeys, ",")})
		out = append(out, &Entity{
			ID:         stableEntityID(snapshotID, "Conflict", conflictNaturalKey),
			Type:       "Conflict",
			NaturalKey: conflictNaturalKey,
			Attributes: map[string]any{
				"entity_type":        entityType,
				"entity_natural_key": naturalKey,
				"conflict_keys":      conflictKeys,
				"observed_values":    observedValues,
				"status":             "unresolved",
				"severity":           "medium",
				"confidence_band":    "low",
			},
			EvidenceIDs: uniqueSorted(evIDs),
			FactIDs:     uniqueSorted(factIDs),
			Confidence:  0.55,
		})
	}
	return out
}

func detectAttributeConflicts(factsForKey []facts.Fact) ([]string, map[string][]string) {
	valueSets := map[string]map[string]struct{}{}
	for _, f := range factsForKey {
		for k, v := range f.Attributes {
			if valueSets[k] == nil {
				valueSets[k] = map[string]struct{}{}
			}
			valueSets[k][fmt.Sprint(v)] = struct{}{}
		}
	}
	conflictKeys := make([]string, 0)
	observed := map[string][]string{}
	for k, set := range valueSets {
		if len(set) < 2 {
			continue
		}
		conflictKeys = append(conflictKeys, k)
		values := make([]string, 0, len(set))
		for v := range set {
			values = append(values, v)
		}
		sort.Strings(values)
		observed[k] = values
	}
	return conflictKeys, observed
}
