package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/sourcefilter"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// Cache only clean Git checkouts. Dirty/non-Git trees, submodules, and symlinks
// are analyzed every time: a Git SHA cannot describe their effective inputs.
func (s *Server) analysisFingerprint(ctx context.Context, pid string, repo store.Repo, opts orchestrator.DiffMindRunOptions, analyzer string) (string, error) {
	if analyzer == "" {
		return "", fmt.Errorf("analyzer identity unavailable")
	}
	root := firstNonEmpty(repo.Path, repo.ClonePath)
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		b, err := cmd.Output()
		return strings.TrimSpace(string(b)), err
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	status, err := git("status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", fmt.Errorf("working tree is dirty")
	}
	files, err := git("ls-files", "--stage")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(files, "\n") {
		if strings.HasPrefix(line, "120000 ") || strings.HasPrefix(line, "160000 ") {
			return "", fmt.Errorf("checkout contains symlinks or submodules")
		}
	}
	project, err := s.store.GetProject(pid)
	if err != nil {
		return "", err
	}
	inputs := map[string]any{"schema": 1, "head": head, "path": root, "analyzer": analyzer, "version": s.version,
		"options": opts, "name": repo.Name, "kind": repo.Kind, "packs": repo.PackIDs, "instruction": repo.Instruction, "project_instruction": project.Instruction}
	// Static analysis reads useful files regardless of Git ignore rules. Include
	// those bytes too (e.g. ignored application configuration), not only HEAD.
	inputs["source_digest"], err = sourceInputDigest(ctx, root)
	if err != nil {
		return "", err
	}
	paths := []string{filepath.Join(root, "diffmind-configuration.yaml"), filepath.Join(root, ".diffmind", "service.yaml"), filepath.Join(s.store.HomeDir(), "config.json"), knowledge.LockPath(s.store.HomeDir())}
	if opts.ConfigPath != "" {
		paths = append(paths, opts.ConfigPath)
	}
	for _, path := range paths {
		body, readErr := os.ReadFile(path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return "", readErr
		}
		inputs[path] = string(body)
	}
	// Verify installed content, not just the lock-file claims.
	packs, err := knowledge.LoadEnabled(s.store.HomeDir())
	if err != nil {
		return "", err
	}
	inputs["installed_packs"], err = knowledge.ActiveSetDigest(packs)
	if err != nil {
		return "", err
	}
	digest, err := knowledge.ContentDigest(s.store.PacksDir(pid))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	inputs["project_packs"] = digest
	body, err := json.Marshal(inputs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func sourceInputDigest(ctx context.Context, root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if sourcefilter.SkipDirName(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if sourcefilter.SkipFileInfo(info) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), info.Size())
		_, err = io.Copy(h, f)
		return err
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Server) analysisArtifactDigest(repo store.Repo) (string, error) {
	info, ok := artifacts.DiffMindRunByID(s.diffmindRunsDir, repo.LastDiffMindRunID)
	if !ok || !artifacts.RunMatchesRepo(info, repo.Name, repo.ID, repo.Path) {
		return "", fmt.Errorf("analysis manifest missing or mismatched")
	}
	if _, err := artifacts.ReadRunDir(info.Dir); err != nil {
		return "", err
	}
	return knowledge.ContentDigest(info.Dir)
}

func (s *Server) reusableAnalysis(repo store.Repo, fingerprint string) bool {
	if fingerprint == "" || repo.AnalysisFingerprint != fingerprint || (repo.SyncStatus != "diffmind_completed" && repo.SyncStatus != "synced") || repo.AnalysisArtifactDigest == "" {
		return false
	}
	digest, err := s.analysisArtifactDigest(repo)
	return err == nil && digest == repo.AnalysisArtifactDigest
}
