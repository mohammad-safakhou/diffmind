package analyzers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

type toolchainAdapterManifest struct {
	Name                      string   `json:"name"`
	Version                   string   `json:"version"`
	Capabilities              []string `json:"capabilities,omitempty"`
	Available                 bool     `json:"available"`
	ToolPath                  string   `json:"tool_path,omitempty"`
	ToolVersion               string   `json:"tool_version,omitempty"`
	ToolchainSHA              string   `json:"toolchain_sha,omitempty"`
	ToolExecStatus            string   `json:"tool_exec_status,omitempty"`
	ToolOutputPath            string   `json:"tool_output_path,omitempty"`
	ToolOutputSHA256          string   `json:"tool_output_sha256,omitempty"`
	ToolSemanticStatus        string   `json:"tool_semantic_status,omitempty"`
	ToolSemanticPath          string   `json:"tool_semantic_path,omitempty"`
	ToolSemanticSHA256        string   `json:"tool_semantic_sha256,omitempty"`
	ToolSemanticFactsAdded    int      `json:"tool_semantic_facts_added,omitempty"`
	ToolSemanticEvidenceAdded int      `json:"tool_semantic_evidence_added,omitempty"`
	Extractors                []string `json:"extractors,omitempty"`
	ReplayKey                 string   `json:"replay_key,omitempty"`
	RunManifestPath           string   `json:"run_manifest_path,omitempty"`
	RunManifestSHA256         string   `json:"run_manifest_sha256,omitempty"`
}

type toolchainManifest struct {
	GeneratedAtUTC  time.Time                  `json:"generated_at_utc"`
	Policy          string                     `json:"policy"`
	Offline         bool                       `json:"offline"`
	SnapshotID      string                     `json:"snapshot_id"`
	SourceRoot      string                     `json:"source_root"`
	Adapters        []toolchainAdapterManifest `json:"adapters"`
	AttestationType string                     `json:"attestation_type"`
	AttestationSHA  string                     `json:"attestation_sha256"`
}

func buildToolchainManifest(report Report) toolchainManifest {
	planned := map[string]AdapterPlanItem{}
	for _, p := range report.AdapterPlan {
		planned[p.Name] = p
	}
	runByName := map[string]AdapterRunItem{}
	for _, r := range report.AdapterRuns {
		runByName[r.Name] = r
	}

	names := make([]string, 0, len(planned))
	for name := range planned {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]toolchainAdapterManifest, 0, len(names))
	for _, name := range names {
		p := planned[name]
		item := toolchainAdapterManifest{
			Name:         p.Name,
			Version:      p.Version,
			Capabilities: append([]string(nil), p.Capabilities...),
			Available:    p.Available,
			ToolPath:     p.ToolPath,
			ToolVersion:  p.ToolVersion,
			ToolchainSHA: p.ToolchainSHA,
			Extractors:   append([]string(nil), p.Extractors...),
		}
		if run, ok := runByName[name]; ok {
			item.ReplayKey = run.ReplayKey
			item.ToolExecStatus = run.ToolExecStatus
			item.ToolOutputPath = run.ToolOutputPath
			item.ToolOutputSHA256 = run.ToolOutputSHA256
			item.ToolSemanticStatus = run.ToolSemanticStatus
			item.ToolSemanticPath = run.ToolSemanticPath
			item.ToolSemanticSHA256 = run.ToolSemanticSHA256
			item.ToolSemanticFactsAdded = run.ToolSemanticFactsAdded
			item.ToolSemanticEvidenceAdded = run.ToolSemanticEvidenceAdded
			item.RunManifestPath = run.RunManifestPath
			item.RunManifestSHA256 = run.RunManifestSHA256
		}
		out = append(out, item)
	}

	m := toolchainManifest{
		GeneratedAtUTC:  time.Now().UTC(),
		Policy:          "self_hosted_offline",
		Offline:         report.Offline,
		SnapshotID:      report.SnapshotID,
		SourceRoot:      report.SourceRoot,
		Adapters:        out,
		AttestationType: "sha256-manifest",
	}
	m.AttestationSHA = manifestAttestationHash(m)
	return m
}

func manifestAttestationHash(m toolchainManifest) string {
	payload := struct {
		Policy     string                     `json:"policy"`
		Offline    bool                       `json:"offline"`
		SnapshotID string                     `json:"snapshot_id"`
		SourceRoot string                     `json:"source_root"`
		Adapters   []toolchainAdapterManifest `json:"adapters"`
	}{
		Policy:     m.Policy,
		Offline:    m.Offline,
		SnapshotID: m.SnapshotID,
		SourceRoot: m.SourceRoot,
		Adapters:   m.Adapters,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeToolchainManifest(outDir string, report *Report) (string, string, error) {
	if report == nil {
		return "", "", fmt.Errorf("nil report")
	}
	manifest := buildToolchainManifest(*report)
	path := filepath.Join(outDir, "analyzers", "toolchain_manifest.json")
	if err := writeJSON(path, manifest); err != nil {
		return "", "", err
	}
	return path, manifest.AttestationSHA, nil
}
