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
}

func analyze(ctx context.Context, root string, forcedSnapshotID string) (result, error) {
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

	files, err := loadFiles(root, inv)
	if err != nil {
		return result{}, err
	}

	c := &collector{
		snapshotID:   snapshotID,
		provenance:   facts.Provenance{AnalyzerID: analyzerID, AnalyzerVersion: analyzerVersion, Deterministic: true, Inferred: false},
		evidenceByID: map[string]facts.Evidence{},
		factByID:     map[string]facts.Fact{},
		report:       Report{GeneratedAt: time.Now().UTC(), SourceRoot: root, SnapshotID: snapshotID},
	}

	for _, f := range files {
		detectRuntimeUnits(c, f)
		detectInboundEndpoints(c, f)
		detectOutboundCalls(c, f)
		detectConfigKeys(c, f)
		detectCIIaC(c, f)
	}

	bundle := c.bundle()
	if err := facts.ValidateBundle(bundle); err != nil {
		return result{}, fmt.Errorf("validate analyzer bundle: %w", err)
	}

	c.report.FactsCount = len(bundle.Facts)
	c.report.EvidenceCount = len(bundle.Evidence)

	return result{bundle: bundle, report: c.report}, nil
}

func loadFiles(root string, inv []snapshot.FileEntry) ([]sourceFile, error) {
	out := make([]sourceFile, 0, len(inv))
	for _, entry := range inv {
		if entry.FileType == "binary" {
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

	fact := facts.NewFact(factType, attrs, []string{ev.ID}, 0.9, c.provenance)
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
	fact := facts.NewFact(factType, attrs, ids, 0.9, c.provenance)
	if _, exists := c.factByID[fact.ID]; !exists {
		c.factByID[fact.ID] = fact
		increment()
	}
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
