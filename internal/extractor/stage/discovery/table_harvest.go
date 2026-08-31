package discovery

import (
	"regexp"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

// table_harvest.go — deterministic physical-table resolution.
//
// A db_operation whose only resource hint is an ORM entity CLASS name
// (details.entity = "TrafficData", no details.table) keys its identity on that
// class name (entitykey.DataResource reads `entity`). That is wrong when the
// physical table differs ("traffic-info"). This pass reads the entity class's
// mapping annotation (@Table(name=...), @Document(collection=...)) from the AST
// and fills details.table with the real name — an identity correction, additive
// and grounded (only when the annotation names it; invariants #4/#6).

// HarvestPhysicalTables fills details.table from an entity class's mapping
// annotation when the op named only the class.
func HarvestPhysicalTables(idx *astpkg.ProjectIndex, deps []model.Dependency) {
	if idx == nil {
		return
	}
	cache := map[string]string{}
	for i := range deps {
		d := &deps[i].BaseEntity
		if d.Type != "db_operation" {
			continue
		}
		entity := scalarDetail(d, "entity")
		if entity == "" {
			continue
		}
		// Already carries a distinct physical table → leave it.
		if t := scalarDetail(d, "table"); t != "" && t != entity {
			continue
		}
		phys, ok := cache[entity]
		if !ok {
			phys = physicalTableForEntity(idx, entity)
			cache[entity] = phys
		}
		if phys == "" || strings.EqualFold(phys, entity) {
			continue
		}
		if d.Details == nil {
			d.Details = map[string]any{}
		}
		if cur := strings.TrimSpace(scalarOf(d.Details["entity"])); cur != "" {
			d.Details["entity_class"] = cur
		}
		d.Details["table"] = phys
	}
}

// physicalTableForEntity finds the class symbol named by entity and returns the
// physical name from its @Table(name=...) / @Document(collection|value=...)
// annotation, or "" when no class or annotation names it.
func physicalTableForEntity(idx *astpkg.ProjectIndex, entity string) string {
	target := strings.ToLower(lastTypeSegment(entity))
	if target == "" {
		return ""
	}
	for _, defs := range idx.Symbols {
		for _, s := range defs {
			if s.Kind != astpkg.SymbolKindClass && s.Kind != astpkg.SymbolKindInterface {
				continue
			}
			if strings.ToLower(s.Name) != target && strings.ToLower(lastTypeSegment(s.Qualified)) != target {
				continue
			}
			for _, ann := range s.Annotations {
				switch ann.Name {
				case "Table":
					if n := annotationAttr(ann.Arguments, "name"); n != "" {
						return n
					}
				case "Document":
					if n := annotationAttr(ann.Arguments, "collection"); n != "" {
						return n
					}
					if n := annotationAttr(ann.Arguments, "value"); n != "" {
						return n
					}
				}
			}
		}
	}
	return ""
}

var soleQuotedRe = regexp.MustCompile(`^\(\s*"([^"]+)"\s*\)$`)

// annotationAttr extracts a named string attribute (attr = "x") from an
// annotation's raw argument text, or a sole positional quoted literal ("x").
func annotationAttr(args, attr string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	if attr != "" {
		re := regexp.MustCompile(regexp.QuoteMeta(attr) + `\s*=\s*"([^"]+)"`)
		if m := re.FindStringSubmatch(args); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	if m := soleQuotedRe.FindStringSubmatch(args); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
