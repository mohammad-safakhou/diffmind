// Package resolver implements deterministic cross-service identity resolution.
// It takes the service registry (with architecture + identity data) and matches
// each service's outbound dependencies to known service identities using
// blueprint-derived aliases and resource identifiers.
//
// Resolution is intentionally deterministic only: there is no fallback or
// external model dependency. The same inputs always produce the same graph.
package resolver

import (
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/registry"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Resolver performs cross-service identity resolution.
type Resolver struct {
	registry *registry.Registry
	log      *util.Logger
}

// New creates a new deterministic resolver.
func New(reg *registry.Registry, log *util.Logger) *Resolver {
	return &Resolver{registry: reg, log: log}
}

// ResolvedMatch represents a single dependency → service match.
type ResolvedMatch struct {
	FromService    string  `json:"from_service"`
	DependencyID   string  `json:"dependency_id"`
	DependencyName string  `json:"dependency_name"`
	DependencyType string  `json:"dependency_type"`
	ToService      string  `json:"to_service"`
	MatchType      string  `json:"match_type"` // http, queue, rpc, shared_db
	Confidence     float64 `json:"confidence"`
	Reasoning      string  `json:"reasoning"`
}

// Resolution holds the full resolution output.
type Resolution struct {
	Matches    []ResolvedMatch        `json:"matches"`
	Unresolved []model.UnresolvedEdge `json:"unresolved"`
}

// Resolve performs deterministic identity resolution across all services.
// Dependencies that do not match any known identity are returned as unresolved
// edges (never guessed).
func (r *Resolver) Resolve() (*Resolution, error) {
	result := &Resolution{}

	entries := r.registry.AllWithArchitecture()
	if len(entries) == 0 {
		return result, nil
	}

	// Build identity index for deterministic matching.
	identityIndex := r.buildIdentityIndex()

	// For each service, try to match its outbound dependencies to known identities.
	for _, entry := range entries {
		if entry.Architecture == nil {
			continue
		}
		for _, dep := range entry.Architecture.Dependencies {
			match := r.tryDeterministicMatch(entry.Name, &dep, identityIndex)
			if match != nil {
				result.Matches = append(result.Matches, *match)
			} else {
				result.Unresolved = append(result.Unresolved, model.UnresolvedEdge{
					Service:        entry.Name,
					DependencyID:   dep.ID,
					DependencyName: dep.Name,
					Type:           dep.Type,
					Target:         extractTarget(&dep),
					Reason:         "no_deterministic_match",
				})
			}
		}
	}

	return result, nil
}

// identityEntry is an index entry for quick lookup.
type identityEntry struct {
	ServiceName string
	Kind        string // dns, iam_role, queue, etc.
	Value       string
}

func (r *Resolver) buildIdentityIndex() []identityEntry {
	var index []identityEntry
	for _, entry := range r.registry.All() {
		if entry.Name != "" {
			index = append(index, identityEntry{
				ServiceName: entry.Name,
				Kind:        "service_name",
				Value:       strings.ToLower(entry.Name),
			})
		}
		if entry.Identity == nil {
			continue
		}
		for _, alias := range entry.Identity.Aliases {
			index = append(index, identityEntry{
				ServiceName: entry.Name,
				Kind:        alias.Kind,
				Value:       strings.ToLower(alias.Value),
			})
		}
		for _, res := range entry.Identity.Resources {
			index = append(index, identityEntry{
				ServiceName: entry.Name,
				Kind:        res.Kind,
				Value:       strings.ToLower(res.Identifier),
			})
		}
	}
	return index
}

func (r *Resolver) tryDeterministicMatch(fromService string, dep *model.Dependency, index []identityEntry) *ResolvedMatch {
	target := strings.ToLower(extractTarget(dep))
	if target == "" {
		return nil
	}

	for _, entry := range index {
		if entry.ServiceName == fromService {
			continue // skip self-references
		}
		if strings.Contains(target, entry.Value) || strings.Contains(entry.Value, target) {
			return &ResolvedMatch{
				FromService:    fromService,
				DependencyID:   dep.ID,
				DependencyName: dep.Name,
				DependencyType: dep.Type,
				ToService:      entry.ServiceName,
				MatchType:      classifyMatchType(dep.Type),
				Confidence:     0.85,
				Reasoning:      fmt.Sprintf("deterministic match: target %q contains identity %q (%s)", target, entry.Value, entry.Kind),
			}
		}
	}
	return nil
}

// extractTarget tries to pull a meaningful target identifier from a dependency.
func extractTarget(dep *model.Dependency) string {
	for _, v := range []string{dep.Instance} {
		if isUsefulTarget(v) {
			return strings.TrimSpace(v)
		}
	}
	// Check details map for common keys.
	if dep.Details != nil {
		for _, key := range []string{
			"url", "base_url", "target_url", "default_url", "production_url",
			"host", "target_host", "target_service", "service",
			"queue", "queue_name", "destination", "topic",
			"database_name", "database", "table_or_entity", "table", "entity", "instance",
		} {
			if v, ok := dep.Details[key]; ok {
				if s := detailString(v); isUsefulTarget(s) {
					return s
				}
			}
		}
	}
	// Check tags for service-like identifiers.
	for _, tag := range dep.Tags {
		if strings.Contains(tag, "-api") || strings.Contains(tag, "-service") || strings.Contains(tag, ".") {
			return tag
		}
	}
	// Fall back to evidence/summary for URL-like patterns.
	for _, ev := range dep.Evidence {
		if strings.Contains(ev.Snippet, "://") || strings.Contains(ev.Snippet, ".internal") || strings.Contains(ev.Snippet, ".global") {
			// Extract URL-like pattern from snippet.
			for _, word := range strings.Fields(ev.Snippet) {
				word = strings.Trim(word, `"'(){},;`)
				if strings.Contains(word, "://") || strings.Contains(word, ".internal") || strings.Contains(word, ".global") {
					return word
				}
			}
		}
	}
	return dep.Name
}

func detailString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func isUsefulTarget(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unknown", "http", "rpc", "database", "cache", "queue", "postgres", "postgresql", "mysql", "redis", "dynamodb", "mongodb":
		return false
	}
	return true
}

func classifyMatchType(depType string) string {
	switch {
	case strings.Contains(depType, "http"):
		return "http"
	case strings.Contains(depType, "rpc") || strings.Contains(depType, "grpc"):
		return "rpc"
	case strings.Contains(depType, "queue") || strings.Contains(depType, "publish"):
		return "queue"
	case strings.Contains(depType, "db") || strings.Contains(depType, "database"):
		return "shared_db"
	default:
		return depType
	}
}
