package analyzers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"diffmind/internal/facts"
)

type adapterSemanticPayload struct {
	Facts []adapterSemanticFact `json:"facts"`
}

type adapterSemanticFact struct {
	Type       string         `json:"type"`
	Attributes map[string]any `json:"attributes"`
	Evidence   struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Col     int    `json:"col"`
		Snippet string `json:"snippet"`
	} `json:"evidence"`
}

type adapterSemanticDocument struct {
	Facts         []adapterSemanticFact         `json:"facts"`
	Symbols       []adapterSemanticSymbol       `json:"symbols"`
	Calls         []adapterSemanticCall         `json:"calls"`
	ExternalCalls []adapterSemanticExternalCall `json:"external_calls"`
	Packages      []adapterSemanticPackage      `json:"packages"`
}

type adapterSemanticPackage struct {
	Name  string                `json:"name"`
	Files []adapterSemanticFile `json:"files"`
}

type adapterSemanticFile struct {
	Path          string                        `json:"path"`
	Symbols       []adapterSemanticSymbol       `json:"symbols"`
	Calls         []adapterSemanticCall         `json:"calls"`
	ExternalCalls []adapterSemanticExternalCall `json:"external_calls"`
	Imports       []adapterSemanticImport       `json:"imports"`
	Dependencies  []adapterSemanticImport       `json:"dependencies"`
}

type adapterSemanticSymbol struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Snippet string `json:"snippet"`
}

type adapterSemanticCall struct {
	Caller  string `json:"caller"`
	Callee  string `json:"callee"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Snippet string `json:"snippet"`
}

type adapterSemanticExternalCall struct {
	Protocol string `json:"protocol"`
	Method   string `json:"method"`
	Target   string `json:"target"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Snippet  string `json:"snippet"`
}

type adapterSemanticImport struct {
	Module  string `json:"module"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Snippet string `json:"snippet"`
}

func mergeAdapterSemanticToolOutputs(sourceRoot string, res *result) error {
	if res == nil {
		return nil
	}
	evidenceByID := map[string]facts.Evidence{}
	for _, ev := range res.bundle.Evidence {
		evidenceByID[ev.ID] = ev
	}
	factByID := map[string]facts.Fact{}
	for _, f := range res.bundle.Facts {
		factByID[f.ID] = f
	}

	for i := range res.report.AdapterRuns {
		run := &res.report.AdapterRuns[i]
		path := strings.TrimSpace(run.ToolSemanticPath)
		if path == "" {
			continue
		}
		payload, err := readAdapterSemanticPayload(path, run.Name)
		if err != nil {
			run.ToolSemanticStatus = "failed"
			continue
		}
		addedFacts := 0
		addedEvidence := 0
		for _, item := range payload.Facts {
			factType := strings.TrimSpace(item.Type)
			if factType == "" {
				continue
			}
			evFile := strings.TrimSpace(item.Evidence.File)
			if evFile == "" {
				evFile = filepath.Join("__adapter__", run.Name+".semantic")
			}
			line := item.Evidence.Line
			if line < 1 {
				line = 1
			}
			col := item.Evidence.Col
			if col < 1 {
				col = 1
			}
			snippet := strings.TrimSpace(item.Evidence.Snippet)
			if snippet == "" {
				snippet = "adapter semantic output"
			}
			if !filepath.IsAbs(evFile) && !strings.HasPrefix(evFile, "__adapter__/") {
				evFile = filepath.ToSlash(filepath.Clean(filepath.Join(sourceRoot, evFile)))
			}
			ev := facts.NewEvidence(res.report.SnapshotID, evFile, line, col, line, col+max(1, len(snippet)), snippet)
			if _, ok := evidenceByID[ev.ID]; !ok {
				evidenceByID[ev.ID] = ev
				addedEvidence++
			}

			attrs := map[string]any{}
			for k, v := range item.Attributes {
				attrs[k] = v
			}
			attrs["adapter_id"] = run.Name
			attrs["adapter_version"] = run.Version
			attrs["toolchain_sha"] = run.ToolchainSHA
			attrs["semantic_source"] = "adapter_tool_output"
			attrs["semantic_tool_output"] = run.ToolSemanticPath

			f := facts.NewFact(factType, attrs, []string{ev.ID}, 0.9, facts.Provenance{
				AnalyzerID:      analyzerID,
				AnalyzerVersion: analyzerVersion,
				Deterministic:   true,
				Inferred:        false,
			})
			if _, ok := factByID[f.ID]; ok {
				continue
			}
			factByID[f.ID] = f
			addedFacts++
		}
		run.ToolSemanticFactsAdded = addedFacts
		run.ToolSemanticEvidenceAdded = addedEvidence
		if run.ToolSemanticStatus == "" {
			run.ToolSemanticStatus = "executed"
		}
	}

	newEvidence := make([]facts.Evidence, 0, len(evidenceByID))
	for _, ev := range evidenceByID {
		newEvidence = append(newEvidence, ev)
	}
	sort.Slice(newEvidence, func(i, j int) bool { return newEvidence[i].ID < newEvidence[j].ID })
	newFacts := make([]facts.Fact, 0, len(factByID))
	for _, f := range factByID {
		newFacts = append(newFacts, f)
	}
	sort.Slice(newFacts, func(i, j int) bool { return newFacts[i].ID < newFacts[j].ID })
	res.bundle.Evidence = newEvidence
	res.bundle.Facts = newFacts
	res.report.FactsCount = len(newFacts)
	res.report.EvidenceCount = len(newEvidence)
	return nil
}

func readAdapterSemanticPayload(path string, adapterName string) (adapterSemanticPayload, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return adapterSemanticPayload{}, err
	}
	var doc adapterSemanticDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return adapterSemanticPayload{}, fmt.Errorf("decode semantic payload: %w", err)
	}
	if len(doc.Facts) > 0 {
		return adapterSemanticPayload{Facts: doc.Facts}, nil
	}
	return normalizeAdapterSemanticDocument(strings.ToLower(strings.TrimSpace(adapterName)), doc), nil
}

func normalizeAdapterSemanticDocument(adapterName string, doc adapterSemanticDocument) adapterSemanticPayload {
	out := adapterSemanticPayload{Facts: make([]adapterSemanticFact, 0)}
	seen := map[string]struct{}{}
	language := adapterLanguage(adapterName)
	addFact := func(f adapterSemanticFact) {
		key := semanticFactDedupKey(f)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out.Facts = append(out.Facts, f)
	}

	for _, sym := range doc.Symbols {
		addFact(symbolToFact(sym, "", language))
	}
	for _, call := range doc.Calls {
		addFact(callToFact(call, "", language))
	}
	for _, ext := range doc.ExternalCalls {
		addFact(externalCallToFact(ext, "", language))
	}
	for _, pkg := range doc.Packages {
		pkgName := strings.TrimSpace(pkg.Name)
		for _, f := range pkg.Files {
			defaultFile := strings.TrimSpace(f.Path)
			for _, sym := range f.Symbols {
				addFact(symbolToFact(sym, defaultFile, language))
			}
			for _, call := range f.Calls {
				addFact(callToFact(call, defaultFile, language))
			}
			for _, ext := range f.ExternalCalls {
				addFact(externalCallToFact(ext, defaultFile, language))
			}
			for _, dep := range f.Imports {
				addFact(importToFact(dep, defaultFile, language))
			}
			for _, dep := range f.Dependencies {
				addFact(importToFact(dep, defaultFile, language))
			}
		}
		_ = pkgName // reserved for future schema enrichments
	}
	return out
}

func symbolToFact(sym adapterSemanticSymbol, defaultFile string, language string) adapterSemanticFact {
	file := strings.TrimSpace(sym.File)
	if file == "" {
		file = defaultFile
	}
	return adapterSemanticFact{
		Type: "CodeSymbol",
		Attributes: map[string]any{
			"name":     strings.TrimSpace(sym.Name),
			"kind":     strings.TrimSpace(sym.Kind),
			"language": language,
		},
		Evidence: struct {
			File    string "json:\"file\""
			Line    int    "json:\"line\""
			Col     int    "json:\"col\""
			Snippet string "json:\"snippet\""
		}{
			File:    file,
			Line:    sym.Line,
			Col:     sym.Col,
			Snippet: strings.TrimSpace(sym.Snippet),
		},
	}
}

func callToFact(call adapterSemanticCall, defaultFile string, language string) adapterSemanticFact {
	file := strings.TrimSpace(call.File)
	if file == "" {
		file = defaultFile
	}
	return adapterSemanticFact{
		Type: "CodeCall",
		Attributes: map[string]any{
			"caller":   strings.TrimSpace(call.Caller),
			"callee":   strings.TrimSpace(call.Callee),
			"kind":     strings.TrimSpace(call.Kind),
			"language": language,
		},
		Evidence: struct {
			File    string "json:\"file\""
			Line    int    "json:\"line\""
			Col     int    "json:\"col\""
			Snippet string "json:\"snippet\""
		}{
			File:    file,
			Line:    call.Line,
			Col:     call.Col,
			Snippet: strings.TrimSpace(call.Snippet),
		},
	}
}

func externalCallToFact(ext adapterSemanticExternalCall, defaultFile string, language string) adapterSemanticFact {
	file := strings.TrimSpace(ext.File)
	if file == "" {
		file = defaultFile
	}
	return adapterSemanticFact{
		Type: "ExternalCall",
		Attributes: map[string]any{
			"protocol": strings.TrimSpace(ext.Protocol),
			"method":   strings.TrimSpace(ext.Method),
			"target":   strings.TrimSpace(ext.Target),
			"language": language,
		},
		Evidence: struct {
			File    string "json:\"file\""
			Line    int    "json:\"line\""
			Col     int    "json:\"col\""
			Snippet string "json:\"snippet\""
		}{
			File:    file,
			Line:    ext.Line,
			Col:     ext.Col,
			Snippet: strings.TrimSpace(ext.Snippet),
		},
	}
}

func importToFact(dep adapterSemanticImport, defaultFile string, language string) adapterSemanticFact {
	file := strings.TrimSpace(dep.File)
	if file == "" {
		file = defaultFile
	}
	module := strings.TrimSpace(dep.Module)
	if module == "" {
		module = strings.TrimSpace(dep.Name)
	}
	return adapterSemanticFact{
		Type: "Dependency",
		Attributes: map[string]any{
			"name":     module,
			"module":   module,
			"version":  strings.TrimSpace(dep.Version),
			"kind":     strings.TrimSpace(dep.Kind),
			"language": language,
		},
		Evidence: struct {
			File    string "json:\"file\""
			Line    int    "json:\"line\""
			Col     int    "json:\"col\""
			Snippet string "json:\"snippet\""
		}{
			File:    file,
			Line:    dep.Line,
			Col:     dep.Col,
			Snippet: strings.TrimSpace(dep.Snippet),
		},
	}
}

func semanticFactDedupKey(f adapterSemanticFact) string {
	attrs, _ := json.Marshal(f.Attributes)
	return strings.Join([]string{
		strings.TrimSpace(f.Type),
		string(attrs),
		strings.TrimSpace(f.Evidence.File),
		fmt.Sprintf("%d:%d", f.Evidence.Line, f.Evidence.Col),
		strings.TrimSpace(f.Evidence.Snippet),
	}, "|")
}

func adapterLanguage(adapterName string) string {
	switch adapterName {
	case "gopls":
		return "go"
	case "tsserver":
		return "typescript"
	case "pyright":
		return "python"
	default:
		return adapterName
	}
}
