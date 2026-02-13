package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func writeArtifact(outRoot string, artifact ParseArtifact) error {
	dir := filepath.Join(outRoot, "parse", artifact.SnapshotID, artifact.FileHash, artifact.ParserVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}
	path := filepath.Join(dir, "artifact.json")
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal parse artifact: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write parse artifact: %w", err)
	}
	return nil
}

func writeReport(outRoot string, report ParseReport) (string, error) {
	dir := filepath.Join(outRoot, "parse")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "report.json")
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
