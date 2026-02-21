package analyzers

import (
	"fmt"
	"sort"
	"strings"
)

// Extractor is the analyzer SDK contract for domain-specific fact extraction.
type Extractor interface {
	Name() string
	Domains() []string
	Extract(c *collector, file sourceFile)
}

type extractorFunc struct {
	name    string
	domains []string
	run     func(c *collector, file sourceFile)
}

func (e extractorFunc) Name() string      { return e.name }
func (e extractorFunc) Domains() []string { return append([]string(nil), e.domains...) }
func (e extractorFunc) Extract(c *collector, file sourceFile) {
	if e.run != nil {
		e.run(c, file)
	}
}

func builtInExtractors() []Extractor {
	return []Extractor{
		extractorFunc{
			name:    "runtime",
			domains: []string{"runtime"},
			run:     detectRuntimeUnits,
		},
		extractorFunc{
			name:    "endpoint",
			domains: []string{"api"},
			run:     detectInboundEndpoints,
		},
		extractorFunc{
			name:    "external_http",
			domains: []string{"api"},
			run:     detectOutboundCalls,
		},
		extractorFunc{
			name:    "queue_db",
			domains: []string{"queue", "db"},
			run:     detectQueueAndDBCalls,
		},
		extractorFunc{
			name:    "config",
			domains: []string{"config"},
			run:     detectConfigKeys,
		},
		extractorFunc{
			name:    "ci_iac",
			domains: []string{"ci", "infra"},
			run:     detectCIIaC,
		},
		extractorFunc{
			name:    "dependency",
			domains: []string{"dependency", "ownership", "risk"},
			run:     detectDependenciesAndOwnership,
		},
		extractorFunc{
			name:    "semantic_model",
			domains: []string{"code_model"},
			run:     detectSemanticSymbolsAndCalls,
		},
	}
}

func resolveExtractors(csv string) ([]Extractor, error) {
	all := builtInExtractors()
	trimmed := strings.TrimSpace(strings.ToLower(csv))
	if trimmed == "" {
		return all, nil
	}
	if trimmed == "none" {
		return []Extractor{}, nil
	}

	byName := make(map[string]Extractor, len(all))
	for _, ex := range all {
		byName[ex.Name()] = ex
	}

	seen := map[string]struct{}{}
	out := make([]Extractor, 0, len(all))
	for _, part := range strings.Split(csv, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		ex, ok := byName[name]
		if !ok {
			names := make([]string, 0, len(byName))
			for n := range byName {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("unsupported extractor %q (supported: %s)", name, strings.Join(names, ","))
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, ex)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no extractors selected")
	}
	return out, nil
}

func extractorNames(extractors []Extractor) []string {
	out := make([]string, 0, len(extractors))
	for _, ex := range extractors {
		out = append(out, ex.Name())
	}
	sort.Strings(out)
	return out
}
