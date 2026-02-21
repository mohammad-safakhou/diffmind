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
	Available            bool
	Reason               string
	ToolPath             string
	ToolVersion          string
	ToolchainFingerprint string
}

type adapterBuiltin struct{}

func (adapterBuiltin) Name() string    { return "builtin" }
func (adapterBuiltin) Version() string { return "v1" }
func (adapterBuiltin) Capabilities() []string {
	return []string{"deterministic", "semantic_ast", "regex_fallback"}
}
func (adapterBuiltin) Probe(_ string) AdapterProbe {
	return AdapterProbe{
		Available:            true,
		Reason:               "built-in adapter available",
		ToolVersion:          analyzerVersion,
		ToolchainFingerprint: analyzerVersion,
	}
}
func (adapterBuiltin) Plan(extractorSelection string) ([]Extractor, error) {
	return resolveExtractors(extractorSelection)
}

type adapterGopls struct{}

func (adapterGopls) Name() string    { return "gopls" }
func (adapterGopls) Version() string { return "v1" }
func (adapterGopls) Capabilities() []string {
	return []string{"go", "lsp", "semantic"}
}
func (adapterGopls) Probe(root string) AdapterProbe {
	return probeGopls(root)
}
func (adapterGopls) Plan(extractorSelection string) ([]Extractor, error) {
	return resolveExtractors(extractorSelection)
}

type adapterTsserver struct{}

func (adapterTsserver) Name() string    { return "tsserver" }
func (adapterTsserver) Version() string { return "v1" }
func (adapterTsserver) Capabilities() []string {
	return []string{"typescript", "javascript", "lsp", "semantic"}
}
func (adapterTsserver) Probe(root string) AdapterProbe {
	return probeTsserver(root)
}
func (adapterTsserver) Plan(extractorSelection string) ([]Extractor, error) {
	return resolveExtractors(extractorSelection)
}

type adapterPyright struct{}

func (adapterPyright) Name() string    { return "pyright" }
func (adapterPyright) Version() string { return "v1" }
func (adapterPyright) Capabilities() []string {
	return []string{"python", "lsp", "semantic"}
}
func (adapterPyright) Probe(root string) AdapterProbe {
	return probePyright(root)
}
func (adapterPyright) Plan(extractorSelection string) ([]Extractor, error) {
	return resolveExtractors(extractorSelection)
}

type adapterJdtls struct{}

func (adapterJdtls) Name() string    { return "jdtls" }
func (adapterJdtls) Version() string { return "v1" }
func (adapterJdtls) Capabilities() []string {
	return []string{"java", "lsp", "semantic"}
}
func (adapterJdtls) Probe(root string) AdapterProbe {
	return probeJdtls(root)
}
func (adapterJdtls) Plan(extractorSelection string) ([]Extractor, error) {
	return resolveExtractors(extractorSelection)
}

func availableAdapters() []Adapter {
	return []Adapter{adapterBuiltin{}, adapterGopls{}, adapterTsserver{}, adapterPyright{}, adapterJdtls{}}
}

func resolveAdapters(csv string) ([]Adapter, error) {
	all := availableAdapters()
	if strings.TrimSpace(csv) == "" {
		return []Adapter{adapterBuiltin{}}, nil
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

func replayKey(snapshotID string, adapterName string, adapterVersion string, toolchainSHA string, extractors []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(snapshotID)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.TrimSpace(adapterName)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.TrimSpace(adapterVersion)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.TrimSpace(toolchainSHA)))
	_, _ = h.Write([]byte("|"))
	names := append([]string(nil), extractors...)
	sort.Strings(names)
	for _, ex := range names {
		_, _ = h.Write([]byte(strings.TrimSpace(ex)))
		_, _ = h.Write([]byte(","))
	}
	return hex.EncodeToString(h.Sum(nil))
}
