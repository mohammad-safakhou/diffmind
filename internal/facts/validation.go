package facts

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrMissingEvidence = errors.New("fact has no evidence")
)

func ValidateBundle(bundle Bundle) error {
	evidenceByID := map[string]Evidence{}
	for _, ev := range bundle.Evidence {
		if err := ValidateEvidence(ev); err != nil {
			return fmt.Errorf("evidence %q invalid: %w", ev.ID, err)
		}
		evidenceByID[ev.ID] = ev
	}

	for _, fact := range bundle.Facts {
		if err := ValidateFact(fact, evidenceByID); err != nil {
			return fmt.Errorf("fact %q invalid: %w", fact.ID, err)
		}
	}

	return nil
}

func ValidateFact(fact Fact, evidenceByID map[string]Evidence) error {
	if strings.TrimSpace(fact.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(fact.Type) == "" {
		return errors.New("type is required")
	}
	if len(fact.EvidenceIDs) == 0 {
		return ErrMissingEvidence
	}
	if fact.Confidence < 0 || fact.Confidence > 1 {
		return errors.New("confidence must be in [0,1]")
	}
	if strings.TrimSpace(fact.Provenance.AnalyzerID) == "" {
		return errors.New("provenance.analyzer_id is required")
	}
	if strings.TrimSpace(fact.Provenance.AnalyzerVersion) == "" {
		return errors.New("provenance.analyzer_version is required")
	}
	for _, evidenceID := range fact.EvidenceIDs {
		if _, ok := evidenceByID[evidenceID]; !ok {
			return fmt.Errorf("evidence reference %q not found", evidenceID)
		}
	}
	return nil
}

func ValidateEvidence(ev Evidence) error {
	if strings.TrimSpace(ev.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(ev.SnapshotID) == "" {
		return errors.New("snapshot_id is required")
	}
	if strings.TrimSpace(ev.FilePath) == "" {
		return errors.New("file_path is required")
	}
	if ev.StartLine < 1 || ev.EndLine < 1 {
		return errors.New("line numbers must be >= 1")
	}
	if ev.StartCol < 1 || ev.EndCol < 1 {
		return errors.New("column numbers must be >= 1")
	}
	if ev.EndLine < ev.StartLine {
		return errors.New("end_line must be >= start_line")
	}
	if ev.EndLine == ev.StartLine && ev.EndCol < ev.StartCol {
		return errors.New("end_col must be >= start_col for same line")
	}
	if strings.TrimSpace(ev.SnippetHash) == "" {
		return errors.New("snippet_hash is required")
	}
	return nil
}

func NewEvidence(snapshotID string, filePath string, startLine int, startCol int, endLine int, endCol int, snippet string) Evidence {
	return Evidence{
		ID:           StableID("evidence", snapshotID, filePath, fmt.Sprintf("%d:%d-%d:%d", startLine, startCol, endLine, endCol), snippet),
		SnapshotID:   snapshotID,
		FilePath:     filePath,
		StartLine:    startLine,
		StartCol:     startCol,
		EndLine:      endLine,
		EndCol:       endCol,
		SnippetHash:  HashSnippet(snippet),
		CreatedAtUTC: time.Now().UTC(),
	}
}

func NewFact(factType string, attributes map[string]any, evidenceIDs []string, confidence float64, provenance Provenance) Fact {
	return Fact{
		ID:           StableID("fact", factType, fmt.Sprint(attributes), strings.Join(evidenceIDs, ","), provenance.AnalyzerID, provenance.AnalyzerVersion),
		Type:         factType,
		Attributes:   attributes,
		EvidenceIDs:  evidenceIDs,
		Confidence:   confidence,
		Provenance:   provenance,
		CreatedAtUTC: time.Now().UTC(),
	}
}
