// Package archfile is the in-repo discovery-file adapter: it reads and writes a
// human-authored YAML catalog (`diffmind.yaml`) that lives inside a target
// repository, and maps it to/from the canonical catalog.Document.
//
// The file is to DiffMind what openapi.yaml is to an HTTP API: a simple,
// version-controlled source of truth a team authors by hand and that automation
// proposes changes to — not the extractor's private storage. It supports a
// `vars:` block (DRY repetition) and `include:` (split a large catalog across
// files), kept deliberately minimal so the format stays easy to author while
// still round-tripping through DiffMind.
//
// The single invariant of this package: identity is computed exactly as the run
// importer computes it. ToModel runs extraction.EnrichEntityGrouping and reuses
// catalog.EntityCatalogKey so a fact authored in the file and the same fact
// discovered by a run collapse to one durable record. Diverge from that and the
// read↔write round trip produces duplicates.
package archfile

// Schema is the value the file's `schema:` key must carry.
const Schema = "diffmind.discovery.v1"

// rawFile mirrors one on-disk YAML document before vars/includes are resolved.
type rawFile struct {
	Schema       string            `yaml:"schema"`
	Service      string            `yaml:"service,omitempty"`
	Team         string            `yaml:"team,omitempty"`
	Vars         map[string]string `yaml:"vars,omitempty"`
	Include      []string          `yaml:"include,omitempty"`
	Resources    []rawResource     `yaml:"resources,omitempty"`
	Exposures    []rawEntity       `yaml:"exposures,omitempty"`
	Dependencies []rawEntity       `yaml:"dependencies,omitempty"`
	Connections  []rawConn         `yaml:"connections,omitempty"`
}

type rawResource struct {
	ID       string         `yaml:"id"`
	Kind     string         `yaml:"kind"`
	Platform string         `yaml:"platform,omitempty"`
	Name     string         `yaml:"name"`
	Instance string         `yaml:"instance,omitempty"`
	Summary  string         `yaml:"summary,omitempty"`
	Tags     []string       `yaml:"tags,omitempty"`
	Details  map[string]any `yaml:"details,omitempty"`
	// Status is the curation state: "verified" (human-confirmed; the default when
	// omitted), "proposed" (automation suggested, awaiting review), or
	// "needs_review". Source records provenance ("manual" or "run:<id>").
	Status string `yaml:"status,omitempty"`
	Source string `yaml:"source,omitempty"`
}

type rawEntity struct {
	// ID is an optional file-local alias used only to wire connections. It is
	// NOT the durable catalog ID, which is derived from semantic identity.
	ID       string         `yaml:"id,omitempty"`
	Type     string         `yaml:"type"`
	Name     string         `yaml:"name"`
	Resource string         `yaml:"resource,omitempty"`
	Service  string         `yaml:"service,omitempty"`
	Summary  string         `yaml:"summary,omitempty"`
	Platform string         `yaml:"platform,omitempty"`
	Tags     []string       `yaml:"tags,omitempty"`
	Details  map[string]any `yaml:"details,omitempty"`
	Status   string         `yaml:"status,omitempty"`
	Source   string         `yaml:"source,omitempty"`
}

type rawConn struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
	// Condition is a shorthand expression; "" means unconditional.
	Condition string `yaml:"condition,omitempty"`
	Summary   string `yaml:"summary,omitempty"`
	Status    string `yaml:"status,omitempty"`
	Source    string `yaml:"source,omitempty"`
}

// File is a fully resolved discovery file: variables expanded, includes inlined,
// service defaults applied. It is what ToModel consumes.
type File struct {
	Service      string
	Team         string
	Resources    []Resource
	Exposures    []Entity
	Dependencies []Entity
	Connections  []Conn
}

// Resource is an explicit top-level cluster in diffmind.yaml. Dependencies may
// point at it with `resource:`; older files can omit resources and let the graph
// derive clusters from dependency identity.
type Resource struct {
	ID       string
	Kind     string
	Platform string
	Name     string
	Instance string
	Summary  string
	Tags     []string
	Details  map[string]any
	Status   string
	Source   string
}

// Entity is one resolved exposure or dependency. Service is already the
// effective service (entity override or file/root default).
type Entity struct {
	Alias    string
	Type     string
	Name     string
	Resource string
	Service  string
	Summary  string
	Platform string
	Tags     []string
	Details  map[string]any
	Status   string
	Source   string
}

// Conn is one resolved connection; From/To reference an entity Alias or Name.
type Conn struct {
	From      string
	To        string
	Condition string
	Summary   string
	Status    string
	Source    string
}
