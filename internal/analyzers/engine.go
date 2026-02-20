package analyzers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"diffmind/internal/facts"
	"diffmind/internal/snapshot"
)

type collector struct {
	snapshotID   string
	provenance   facts.Provenance
	evidenceByID map[string]facts.Evidence
	factByID     map[string]facts.Fact
	report       Report
	adapterName  string
	adapterVer   string
	toolchainSHA string
}

func analyze(ctx context.Context, root string, forcedSnapshotID string, adapterSelection string, extractorSelection string, includeTests bool, allowMissingAdapters bool) (result, error) {
	_ = ctx
	inv, err := snapshot.BuildInventory(root, snapshot.InventoryOptions{ExcludeDirs: map[string]struct{}{
		".git": {}, ".diffmind": {}, ".gocache": {}, "bin": {}, "node_modules": {},
	}})
	if err != nil {
		return result{}, fmt.Errorf("build inventory for analyzers: %w", err)
	}

	snapshotID := forcedSnapshotID
	if snapshotID == "" {
		snapshotID = deriveSnapshotID(root, inv)
	}

	files, err := loadFiles(root, inv, includeTests)
	if err != nil {
		return result{}, err
	}
	adapters, err := resolveAdapters(adapterSelection)
	if err != nil {
		return result{}, err
	}

	c := &collector{
		snapshotID:   snapshotID,
		provenance:   facts.Provenance{AnalyzerID: analyzerID, AnalyzerVersion: analyzerVersion, Deterministic: true, Inferred: false},
		evidenceByID: map[string]facts.Evidence{},
		factByID:     map[string]facts.Fact{},
		report: Report{
			GeneratedAt: time.Now().UTC(),
			SourceRoot:  root,
			SnapshotID:  snapshotID,
			Adapters:    adapterNames(adapters),
		},
	}

	executedExtractors := map[string]struct{}{}
	for _, ad := range adapters {
		probe := ad.Probe(root)
		planItem := AdapterPlanItem{
			Name:         ad.Name(),
			Version:      ad.Version(),
			Capabilities: ad.Capabilities(),
			Available:    probe.Available,
			Selected:     true,
			Reason:       strings.TrimSpace(probe.Reason),
			ToolPath:     strings.TrimSpace(probe.ToolPath),
			ToolVersion:  strings.TrimSpace(probe.ToolVersion),
			ToolchainSHA: strings.TrimSpace(probe.ToolchainFingerprint),
		}
		if !probe.Available {
			if strings.TrimSpace(adapterSelection) != "" && !allowMissingAdapters {
				return result{}, fmt.Errorf("adapter %q unavailable: %s (rerun with --allow-missing-adapters to continue)", ad.Name(), strings.TrimSpace(probe.Reason))
			}
			c.report.AdapterPlan = append(c.report.AdapterPlan, planItem)
			continue
		}
		extractors, err := ad.Plan(extractorSelection)
		if err != nil {
			return result{}, err
		}
		planItem.Extractors = extractorNames(extractors)
		c.report.AdapterPlan = append(c.report.AdapterPlan, planItem)
		if len(extractors) == 0 {
			continue
		}

		beforeFacts := len(c.factByID)
		beforeEvidence := len(c.evidenceByID)
		c.adapterName = ad.Name()
		c.adapterVer = ad.Version()
		c.toolchainSHA = strings.TrimSpace(probe.ToolchainFingerprint)
		for _, f := range files {
			for _, ex := range extractors {
				ex.Extract(c, f)
				executedExtractors[ex.Name()] = struct{}{}
			}
		}
		if extractorSelected(extractors, "config") {
			detectSpringProfileResolvedConfig(c, files)
		}
		run := AdapterRunItem{
			Name:          ad.Name(),
			Version:       ad.Version(),
			ToolPath:      strings.TrimSpace(probe.ToolPath),
			ToolVersion:   strings.TrimSpace(probe.ToolVersion),
			ToolchainSHA:  strings.TrimSpace(probe.ToolchainFingerprint),
			Extractors:    extractorNames(extractors),
			FactsAdded:    len(c.factByID) - beforeFacts,
			EvidenceAdded: len(c.evidenceByID) - beforeEvidence,
			ReplayKey:     replayKey(snapshotID, ad.Name(), ad.Version(), strings.TrimSpace(probe.ToolchainFingerprint), extractorNames(extractors)),
		}
		c.report.AdapterRuns = append(c.report.AdapterRuns, run)
	}

	if len(executedExtractors) > 0 {
		names := make([]string, 0, len(executedExtractors))
		for name := range executedExtractors {
			names = append(names, name)
		}
		sort.Strings(names)
		c.report.Extractors = names
	}

	bundle := c.bundle()
	if err := facts.ValidateBundle(bundle); err != nil {
		return result{}, fmt.Errorf("validate analyzer bundle: %w", err)
	}

	c.report.FactsCount = len(bundle.Facts)
	c.report.EvidenceCount = len(bundle.Evidence)

	return result{bundle: bundle, report: c.report}, nil
}

func extractorSelected(extractors []Extractor, name string) bool {
	target := strings.TrimSpace(strings.ToLower(name))
	for _, ex := range extractors {
		if strings.TrimSpace(strings.ToLower(ex.Name())) == target {
			return true
		}
	}
	return false
}

func loadFiles(root string, inv []snapshot.FileEntry, includeTests bool) ([]sourceFile, error) {
	out := make([]sourceFile, 0, len(inv))
	for _, entry := range inv {
		if entry.FileType == "binary" {
			continue
		}
		if !includeTests && isTestSourcePath(entry.Path) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(entry.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Path, err)
		}
		if isLikelyBinary(data) {
			continue
		}
		text := string(data)
		lines := strings.Split(text, "\n")
		out = append(out, sourceFile{Path: entry.Path, AbsPath: abs, Ext: strings.ToLower(filepath.Ext(entry.Path)), Lines: lines, Text: text})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func isTestSourcePath(path string) bool {
	p := strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "src/test/") || strings.HasPrefix(p, "src/tests/") || strings.Contains(p, "/src/test/") || strings.Contains(p, "/src/tests/") || strings.Contains(p, "/__tests__/") {
		return true
	}
	base := filepath.Base(p)
	return strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".spec.js") || strings.HasSuffix(base, ".spec.ts")
}

func (c *collector) addFactWithEvidence(factType string, attrs map[string]any, file sourceFile, line int, col int, snippet string, increment func()) {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	endCol := col + max(1, len(snippet))
	ev := facts.NewEvidence(c.snapshotID, file.Path, line, col, line, endCol, snippet)
	c.evidenceByID[ev.ID] = ev

	fact := facts.NewFact(factType, c.normalizeAttrs(attrs), []string{ev.ID}, 0.9, c.provenance)
	if _, exists := c.factByID[fact.ID]; !exists {
		c.factByID[fact.ID] = fact
		increment()
	}
}

func (c *collector) addFactMultiEvidence(factType string, attrs map[string]any, evidence []facts.Evidence, increment func()) {
	ids := make([]string, 0, len(evidence))
	for _, ev := range evidence {
		c.evidenceByID[ev.ID] = ev
		ids = append(ids, ev.ID)
	}
	if len(ids) == 0 {
		return
	}
	fact := facts.NewFact(factType, c.normalizeAttrs(attrs), ids, 0.9, c.provenance)
	if _, exists := c.factByID[fact.ID]; !exists {
		c.factByID[fact.ID] = fact
		increment()
	}
}

func (c *collector) normalizeAttrs(attrs map[string]any) map[string]any {
	if attrs == nil {
		attrs = map[string]any{}
	}
	if strings.TrimSpace(c.adapterName) != "" {
		if _, ok := attrs["adapter_id"]; !ok {
			attrs["adapter_id"] = c.adapterName
		}
		if _, ok := attrs["provenance_adapter_id"]; !ok {
			attrs["provenance_adapter_id"] = c.adapterName
		}
	}
	if strings.TrimSpace(c.adapterVer) != "" {
		if _, ok := attrs["adapter_version"]; !ok {
			attrs["adapter_version"] = c.adapterVer
		}
		if _, ok := attrs["provenance_version"]; !ok {
			attrs["provenance_version"] = c.adapterVer
		}
	}
	if strings.TrimSpace(c.toolchainSHA) != "" {
		if _, ok := attrs["toolchain_sha"]; !ok {
			attrs["toolchain_sha"] = c.toolchainSHA
		}
		if _, ok := attrs["provenance_toolchain_sha"]; !ok {
			attrs["provenance_toolchain_sha"] = c.toolchainSHA
		}
	}
	return attrs
}

func (c *collector) bundle() facts.Bundle {
	evidence := make([]facts.Evidence, 0, len(c.evidenceByID))
	for _, ev := range c.evidenceByID {
		evidence = append(evidence, ev)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })

	factsOut := make([]facts.Fact, 0, len(c.factByID))
	for _, f := range c.factByID {
		factsOut = append(factsOut, f)
	}
	sort.Slice(factsOut, func(i, j int) bool { return factsOut[i].ID < factsOut[j].ID })

	return facts.Bundle{Facts: factsOut, Evidence: evidence, Generated: time.Now().UTC()}
}

func deriveSnapshotID(root string, inv []snapshot.FileEntry) string {
	h := sha256.New()
	_, _ = h.Write([]byte(root))
	_, _ = h.Write([]byte("|" + analyzerVersion))
	for _, f := range inv {
		_, _ = h.Write([]byte(f.Path))
		_, _ = h.Write([]byte("|"))
		_, _ = h.Write([]byte(f.SHA256))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func isLikelyBinary(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

func regexMatchesByLine(lines []string, expr *regexp.Regexp) []lineMatch {
	out := make([]lineMatch, 0)
	for idx, line := range lines {
		locs := expr.FindAllStringSubmatchIndex(line, -1)
		for _, loc := range locs {
			groups := make([]string, 0)
			for i := 2; i < len(loc); i += 2 {
				if loc[i] == -1 || loc[i+1] == -1 {
					groups = append(groups, "")
					continue
				}
				groups = append(groups, line[loc[i]:loc[i+1]])
			}
			out = append(out, lineMatch{line: idx + 1, col: loc[0] + 1, text: line[loc[0]:loc[1]], groups: groups})
		}
	}
	return out
}

type lineMatch struct {
	line   int
	col    int
	text   string
	groups []string
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
