package analyzers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Adapter is the analyzer runtime contract for pluggable tool-backed extractors.
type Adapter interface {
	Name() string
	Version() string
	Capabilities() []string
	Probe(root string) AdapterProbe
	Plan(extractorSelection string) ([]Extractor, error)
}

type AdapterProbe struct {
	Available bool
	Reason    string
}

type adapterBuiltin struct{}

func (adapterBuiltin) Name() string    { return "builtin" }
func (adapterBuiltin) Version() string { return "v1" }
func (adapterBuiltin) Capabilities() []string {
	return []string{"deterministic", "semantic_ast", "regex_fallback"}
}
func (adapterBuiltin) Probe(_ string) AdapterProbe {
	return AdapterProbe{Available: true, Reason: "built-in adapter available"}
}
func (adapterBuiltin) Plan(extractorSelection string) ([]Extractor, error) {
	return resolveExtractors(extractorSelection)
}

func builtInAdapters() []Adapter {
	return []Adapter{adapterBuiltin{}}
}

func resolveAdapters(csv string) ([]Adapter, error) {
	all := builtInAdapters()
	if strings.TrimSpace(csv) == "" {
		return all, nil
	}

	byName := make(map[string]Adapter, len(all))
	for _, ad := range all {
		byName[ad.Name()] = ad
	}

	seen := map[string]struct{}{}
	out := make([]Adapter, 0, len(all))
	for _, part := range strings.Split(csv, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		ad, ok := byName[name]
		if !ok {
			names := make([]string, 0, len(byName))
			for n := range byName {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("unsupported adapter %q (supported: %s)", name, strings.Join(names, ","))
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, ad)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no adapters selected")
	}
	return out, nil
}

func adapterNames(adapters []Adapter) []string {
	out := make([]string, 0, len(adapters))
	for _, ad := range adapters {
		out = append(out, ad.Name())
	}
	sort.Strings(out)
	return out
}

func replayKey(snapshotID string, adapterName string, adapterVersion string, extractors []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(snapshotID)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.TrimSpace(adapterName)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.TrimSpace(adapterVersion)))
	_, _ = h.Write([]byte("|"))
	names := append([]string(nil), extractors...)
	sort.Strings(names)
	for _, ex := range names {
		_, _ = h.Write([]byte(strings.TrimSpace(ex)))
		_, _ = h.Write([]byte(","))
	}
	return hex.EncodeToString(h.Sum(nil))
}
