package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/entitykey"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

var ErrRevisionConflict = errors.New("architecture revision conflict")

type Store struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

func NewStore(baseDir string) *Store {
	return &Store{
		path: filepath.Join(baseDir, "architecture.v1.json"),
		now:  time.Now,
	}
}

func (s *Store) Path() string { return s.path }

func (s *Store) Load() (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// SaveManual replaces the editable graph using optimistic concurrency. Record
// metadata and import history are server-owned: changed/new records become
// manual, unchanged records retain their previous owner.
func (s *Store) SaveManual(in Document) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.loadLocked()
	if err != nil {
		return Document{}, err
	}
	if in.Revision != current.Revision {
		return Document{}, fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, in.Revision, current.Revision)
	}

	now := s.now().UTC()
	in.SchemaVersion = SchemaVersion
	in.Revision = current.Revision + 1
	in.UpdatedAt = now
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = current.Name
	}
	in.Imports = append([]ImportRecord(nil), current.Imports...)
	normalizeDocument(&in)
	in.Records = manualMetadata(current, in, now)
	if err := Validate(in); err != nil {
		return Document{}, err
	}
	if err := s.writeLocked(in); err != nil {
		return Document{}, err
	}
	return in, nil
}

// Import merges one extraction run into the canonical catalog. Semantic
// identity, not run-local IDs, chooses the durable record. Automation can
// refresh automation-owned records but never overwrites manually owned ones.
func (s *Store) Import(in ImportInput) (Document, ImportSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked()
	if err != nil {
		return Document{}, ImportSummary{}, err
	}
	if strings.TrimSpace(in.RunID) == "" {
		return Document{}, ImportSummary{}, errors.New("run_id is required")
	}

	now := s.now().UTC()
	summary := ImportSummary{RunID: in.RunID}
	idMap := map[string]string{}

	doc.Exposures = mergeExposures(doc.Exposures, in.Exposures, doc.Records, idMap, in.RunID, now, &summary)
	doc.Dependencies = mergeDependencies(doc.Dependencies, in.Dependencies, doc.Records, idMap, in.RunID, now, &summary)
	doc.Connections = mergeConnections(doc.Connections, in.Connections, doc.Records, idMap, in.RunID, now, &summary)

	doc.SchemaVersion = SchemaVersion
	doc.Revision++
	doc.UpdatedAt = now
	doc.Imports = append(doc.Imports, ImportRecord{
		RunID:         in.RunID,
		ImportedAt:    now,
		Added:         summary.Added,
		Updated:       summary.Updated,
		SkippedManual: summary.SkippedManual,
	})
	normalizeDocument(&doc)
	if err := Validate(doc); err != nil {
		return Document{}, ImportSummary{}, err
	}
	if err := s.writeLocked(doc); err != nil {
		return Document{}, ImportSummary{}, err
	}
	return doc, summary, nil
}

func (s *Store) loadLocked() (Document, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyDocument(), nil
	}
	if err != nil {
		return Document{}, err
	}
	var doc Document
	if err := json.Unmarshal(b, &doc); err != nil {
		return Document{}, fmt.Errorf("parse architecture catalog: %w", err)
	}
	if doc.SchemaVersion != "" && doc.SchemaVersion != SchemaVersion {
		return Document{}, fmt.Errorf("unsupported architecture schema %q", doc.SchemaVersion)
	}
	normalizeDocument(&doc)
	if err := Validate(doc); err != nil {
		return Document{}, fmt.Errorf("invalid architecture catalog: %w", err)
	}
	return doc, nil
}

func (s *Store) writeLocked(doc Document) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(s.path), ".architecture-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func Validate(doc Document) error {
	ids := map[string]string{}
	for _, e := range doc.Exposures {
		if err := validateEntity("exposure", e.BaseEntity, ids); err != nil {
			return err
		}
	}
	for _, d := range doc.Dependencies {
		if err := validateEntity("dependency", d.BaseEntity, ids); err != nil {
			return err
		}
	}
	for _, c := range doc.Connections {
		if strings.TrimSpace(c.ID) == "" {
			return errors.New("connection id is required")
		}
		if previous, exists := ids[c.ID]; exists {
			return fmt.Errorf("duplicate id %q used by %s and connection", c.ID, previous)
		}
		ids[c.ID] = "connection"
		if ids[c.FromExposureID] != "exposure" {
			return fmt.Errorf("connection %q references unknown exposure %q", c.ID, c.FromExposureID)
		}
		if ids[c.ToDependencyID] != "dependency" {
			return fmt.Errorf("connection %q references unknown dependency %q", c.ID, c.ToDependencyID)
		}
	}
	return nil
}

func validateEntity(kind string, b model.BaseEntity, ids map[string]string) error {
	if strings.TrimSpace(b.ID) == "" {
		return fmt.Errorf("%s id is required", kind)
	}
	if strings.TrimSpace(b.Type) == "" {
		return fmt.Errorf("%s %q type is required", kind, b.ID)
	}
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("%s %q name is required", kind, b.ID)
	}
	if previous, exists := ids[b.ID]; exists {
		return fmt.Errorf("duplicate id %q used by %s and %s", b.ID, previous, kind)
	}
	ids[b.ID] = kind
	return nil
}

func normalizeDocument(doc *Document) {
	doc.SchemaVersion = SchemaVersion
	if doc.Name == "" {
		doc.Name = "DiffMind Architecture"
	}
	if doc.Exposures == nil {
		doc.Exposures = []model.Exposure{}
	}
	if doc.Dependencies == nil {
		doc.Dependencies = []model.Dependency{}
	}
	if doc.Connections == nil {
		doc.Connections = []model.Connection{}
	}
	if doc.Records == nil {
		doc.Records = map[string]RecordMetadata{}
	}
	if doc.Imports == nil {
		doc.Imports = []ImportRecord{}
	}
	for i := range doc.Exposures {
		normalizeEntity(&doc.Exposures[i].BaseEntity, "exposure")
	}
	for i := range doc.Dependencies {
		normalizeEntity(&doc.Dependencies[i].BaseEntity, "dependency")
	}
	for i := range doc.Connections {
		normalizeConnection(&doc.Connections[i])
	}
	sort.Slice(doc.Exposures, func(i, j int) bool { return doc.Exposures[i].ID < doc.Exposures[j].ID })
	sort.Slice(doc.Dependencies, func(i, j int) bool { return doc.Dependencies[i].ID < doc.Dependencies[j].ID })
	sort.Slice(doc.Connections, func(i, j int) bool { return doc.Connections[i].ID < doc.Connections[j].ID })
}

func normalizeEntity(b *model.BaseEntity, kind string) {
	b.Type = strings.TrimSpace(b.Type)
	b.Name = strings.TrimSpace(b.Name)
	b.Service = strings.TrimSpace(b.Service)
	b.Summary = strings.TrimSpace(b.Summary)
	if b.ID == "" && b.Type != "" && b.Name != "" {
		b.ID = util.StableID("architecture", kind, b.Service, b.Type, b.Name)
	}
	if b.Confidence == 0 {
		b.Confidence = 1
	}
	if b.Locations == nil {
		b.Locations = []model.Location{}
	}
	if b.Evidence == nil {
		b.Evidence = []model.Evidence{}
	}
	if b.Details == nil {
		b.Details = map[string]any{}
	}
}

func normalizeConnection(c *model.Connection) {
	if c.ID == "" && c.FromExposureID != "" && c.ToDependencyID != "" {
		c.ID = util.StableID("architecture-connection", c.FromExposureID, c.ToDependencyID, c.PathSignature)
	}
	if c.Source == "" {
		c.Source = "manual"
	}
	if c.Confidence == 0 {
		c.Confidence = 1
	}
	if c.Condition.Kind == "" {
		c.Condition = model.Condition{Kind: "unconditional", Expression: "true", Explanation: "Always"}
	}
	if c.PathSignature == "" {
		c.PathSignature = "manual:" + c.FromExposureID + "->" + c.ToDependencyID
	}
	if c.Locations == nil {
		c.Locations = []model.Location{}
	}
	if c.Evidence == nil {
		c.Evidence = []model.Evidence{}
	}
	if c.Paths == nil {
		c.Paths = []model.ConnectionPath{}
	}
}

func manualMetadata(current, incoming Document, now time.Time) map[string]RecordMetadata {
	out := map[string]RecordMetadata{}
	currentRecords := recordValues(current)
	incomingRecords := recordValues(incoming)
	for id, value := range incomingRecords {
		meta, exists := current.Records[id]
		if exists && reflect.DeepEqual(currentRecords[id], value) {
			out[id] = meta
			continue
		}
		if !exists || meta.CreatedAt.IsZero() {
			meta.CreatedAt = now
		}
		meta.Owner = OwnerManual
		meta.RunID = ""
		meta.UpdatedAt = now
		out[id] = meta
	}
	return out
}

func recordValues(doc Document) map[string]any {
	out := map[string]any{}
	for _, e := range doc.Exposures {
		out[e.ID] = e
	}
	for _, d := range doc.Dependencies {
		out[d.ID] = d
	}
	for _, c := range doc.Connections {
		out[c.ID] = c
	}
	return out
}

func mergeExposures(current, incoming []model.Exposure, metadata map[string]RecordMetadata, idMap map[string]string, runID string, now time.Time, summary *ImportSummary) []model.Exposure {
	byKey := map[string]int{}
	for i, e := range current {
		byKey[entityCatalogKey("exposure", e.BaseEntity)] = i
	}
	for _, candidate := range incoming {
		oldID := candidate.ID
		key := entityCatalogKey("exposure", candidate.BaseEntity)
		if i, ok := byKey[key]; ok {
			idMap[oldID] = current[i].ID
			if metadata[current[i].ID].Owner == OwnerManual {
				summary.SkippedManual++
				continue
			}
			candidate.ID = current[i].ID
			current[i] = candidate
			setAutomationMetadata(metadata, candidate.ID, runID, now)
			summary.Updated++
			continue
		}
		candidate.ID = util.StableID("architecture", "exposure", key)
		idMap[oldID] = candidate.ID
		current = append(current, candidate)
		byKey[key] = len(current) - 1
		setAutomationMetadata(metadata, candidate.ID, runID, now)
		summary.Added++
	}
	return current
}

func mergeDependencies(current, incoming []model.Dependency, metadata map[string]RecordMetadata, idMap map[string]string, runID string, now time.Time, summary *ImportSummary) []model.Dependency {
	byKey := map[string]int{}
	for i, d := range current {
		byKey[entityCatalogKey("dependency", d.BaseEntity)] = i
	}
	for _, candidate := range incoming {
		oldID := candidate.ID
		key := entityCatalogKey("dependency", candidate.BaseEntity)
		if i, ok := byKey[key]; ok {
			idMap[oldID] = current[i].ID
			if metadata[current[i].ID].Owner == OwnerManual {
				summary.SkippedManual++
				continue
			}
			candidate.ID = current[i].ID
			current[i] = candidate
			setAutomationMetadata(metadata, candidate.ID, runID, now)
			summary.Updated++
			continue
		}
		candidate.ID = util.StableID("architecture", "dependency", key)
		idMap[oldID] = candidate.ID
		current = append(current, candidate)
		byKey[key] = len(current) - 1
		setAutomationMetadata(metadata, candidate.ID, runID, now)
		summary.Added++
	}
	return current
}

func mergeConnections(current, incoming []model.Connection, metadata map[string]RecordMetadata, idMap map[string]string, runID string, now time.Time, summary *ImportSummary) []model.Connection {
	byKey := map[string]int{}
	for i, c := range current {
		byKey[connectionCatalogKey(c)] = i
	}
	for _, candidate := range incoming {
		fromID, fromOK := idMap[candidate.FromExposureID]
		toID, toOK := idMap[candidate.ToDependencyID]
		if !fromOK || !toOK {
			continue
		}
		candidate.FromExposureID = fromID
		candidate.ToDependencyID = toID
		key := connectionCatalogKey(candidate)
		if i, ok := byKey[key]; ok {
			if metadata[current[i].ID].Owner == OwnerManual {
				summary.SkippedManual++
				continue
			}
			candidate.ID = current[i].ID
			current[i] = candidate
			setAutomationMetadata(metadata, candidate.ID, runID, now)
			summary.Updated++
			continue
		}
		candidate.ID = util.StableID("architecture-connection", key)
		current = append(current, candidate)
		byKey[key] = len(current) - 1
		setAutomationMetadata(metadata, candidate.ID, runID, now)
		summary.Added++
	}
	return current
}

func entityCatalogKey(kind string, b model.BaseEntity) string {
	return strings.Join([]string{
		kind,
		strings.ToLower(strings.TrimSpace(b.Service)),
		entitykey.SemanticLoose(b),
	}, "|")
}

func connectionCatalogKey(c model.Connection) string {
	return strings.Join([]string{c.FromExposureID, c.ToDependencyID, strings.ToLower(strings.TrimSpace(c.PathSignature))}, "|")
}

func setAutomationMetadata(metadata map[string]RecordMetadata, id, runID string, now time.Time) {
	meta := metadata[id]
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.Owner = OwnerAutomation
	meta.RunID = runID
	meta.UpdatedAt = now
	metadata[id] = meta
}
