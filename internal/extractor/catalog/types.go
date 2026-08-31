// Package catalog owns DiffMind's canonical, editable architecture document.
// Extraction runs are inputs to this package; they are not the durable product.
package catalog

import (
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

const SchemaVersion = "architecture.v1"

type Owner string

const (
	OwnerManual     Owner = "manual"
	OwnerAutomation Owner = "automation"
)

// RecordMetadata tracks who owns the current value of a node or connection.
// The first implementation uses record-level ownership. A manual edit protects
// the whole record from later automation imports; field-level ownership can be
// added without changing the graph records themselves.
type RecordMetadata struct {
	Owner     Owner     `json:"owner"`
	RunID     string    `json:"run_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ImportRecord struct {
	RunID         string    `json:"run_id"`
	ImportedAt    time.Time `json:"imported_at"`
	Added         int       `json:"added"`
	Updated       int       `json:"updated"`
	SkippedManual int       `json:"skipped_manual"`
}

// Document is the source-of-truth architecture database exposed by the API and
// editor. Revision is an optimistic-concurrency token, not a display counter.
type Document struct {
	SchemaVersion string                    `json:"schema_version"`
	Revision      int64                     `json:"revision"`
	Name          string                    `json:"name"`
	Description   string                    `json:"description,omitempty"`
	UpdatedAt     time.Time                 `json:"updated_at"`
	Exposures     []model.Exposure          `json:"exposures"`
	Dependencies  []model.Dependency        `json:"dependencies"`
	Connections   []model.Connection        `json:"connections"`
	Records       map[string]RecordMetadata `json:"records,omitempty"`
	Imports       []ImportRecord            `json:"imports,omitempty"`
}

type ImportInput struct {
	RunID        string
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
}

type ImportSummary struct {
	RunID         string `json:"run_id"`
	Added         int    `json:"added"`
	Updated       int    `json:"updated"`
	SkippedManual int    `json:"skipped_manual"`
}

func EmptyDocument() Document {
	return Document{
		SchemaVersion: SchemaVersion,
		Name:          "DiffMind Architecture",
		Exposures:     []model.Exposure{},
		Dependencies:  []model.Dependency{},
		Connections:   []model.Connection{},
		Records:       map[string]RecordMetadata{},
		Imports:       []ImportRecord{},
	}
}
