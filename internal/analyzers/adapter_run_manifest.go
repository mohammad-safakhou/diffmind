package analyzers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type adapterRunManifest struct {
	SnapshotID                string   `json:"snapshot_id"`
	SourceRoot                string   `json:"source_root"`
	AdapterName               string   `json:"adapter_name"`
	AdapterVersion            string   `json:"adapter_version"`
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
	FactsAdded                int      `json:"facts_added"`
	EvidenceAdded             int      `json:"evidence_added"`
	ReplayKey                 string   `json:"replay_key"`
	AttestationType           string   `json:"attestation_type"`
	AttestationSHA256         string   `json:"attestation_sha256"`
}

func writeAdapterRunManifests(outDir string, report *Report) error {
	if report == nil {
		return fmt.Errorf("nil report")
	}
	for i := range report.AdapterRuns {
		run := &report.AdapterRuns[i]
		manifest := adapterRunManifest{
			SnapshotID:                report.SnapshotID,
			SourceRoot:                report.SourceRoot,
			AdapterName:               run.Name,
			AdapterVersion:            run.Version,
			ToolPath:                  run.ToolPath,
			ToolVersion:               run.ToolVersion,
			ToolchainSHA:              run.ToolchainSHA,
			ToolExecStatus:            run.ToolExecStatus,
			ToolOutputPath:            run.ToolOutputPath,
			ToolOutputSHA256:          run.ToolOutputSHA256,
			ToolSemanticStatus:        run.ToolSemanticStatus,
			ToolSemanticPath:          run.ToolSemanticPath,
			ToolSemanticSHA256:        run.ToolSemanticSHA256,
			ToolSemanticFactsAdded:    run.ToolSemanticFactsAdded,
			ToolSemanticEvidenceAdded: run.ToolSemanticEvidenceAdded,
			Extractors:                append([]string(nil), run.Extractors...),
			FactsAdded:                run.FactsAdded,
			EvidenceAdded:             run.EvidenceAdded,
			ReplayKey:                 run.ReplayKey,
			AttestationType:           "sha256-manifest",
		}
		manifest.AttestationSHA256 = adapterRunManifestHash(manifest)

		name := strings.TrimSpace(run.Name)
		if name == "" {
			name = "unknown"
		}
		path := filepath.Join(outDir, "analyzers", "runs", name+".json")
		if err := writeJSON(path, manifest); err != nil {
			return err
		}
		run.RunManifestPath = path
		run.RunManifestSHA256 = manifest.AttestationSHA256
	}
	return nil
}

func adapterRunManifestHash(m adapterRunManifest) string {
	payload := struct {
		SnapshotID                string   `json:"snapshot_id"`
		SourceRoot                string   `json:"source_root"`
		AdapterName               string   `json:"adapter_name"`
		AdapterVersion            string   `json:"adapter_version"`
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
		FactsAdded                int      `json:"facts_added"`
		EvidenceAdded             int      `json:"evidence_added"`
		ReplayKey                 string   `json:"replay_key"`
		AttestationType           string   `json:"attestation_type"`
	}{
		SnapshotID:                m.SnapshotID,
		SourceRoot:                m.SourceRoot,
		AdapterName:               m.AdapterName,
		AdapterVersion:            m.AdapterVersion,
		ToolPath:                  m.ToolPath,
		ToolVersion:               m.ToolVersion,
		ToolchainSHA:              m.ToolchainSHA,
		ToolExecStatus:            m.ToolExecStatus,
		ToolOutputPath:            m.ToolOutputPath,
		ToolOutputSHA256:          m.ToolOutputSHA256,
		ToolSemanticStatus:        m.ToolSemanticStatus,
		ToolSemanticPath:          m.ToolSemanticPath,
		ToolSemanticSHA256:        m.ToolSemanticSHA256,
		ToolSemanticFactsAdded:    m.ToolSemanticFactsAdded,
		ToolSemanticEvidenceAdded: m.ToolSemanticEvidenceAdded,
		Extractors:                m.Extractors,
		FactsAdded:                m.FactsAdded,
		EvidenceAdded:             m.EvidenceAdded,
		ReplayKey:                 m.ReplayKey,
		AttestationType:           m.AttestationType,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
