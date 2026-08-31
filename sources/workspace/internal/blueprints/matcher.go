package blueprints

import (
	"os"
	"path/filepath"
	"strings"
)

// Matches checks if a blueprint applies to a given repository.
func Matches(bp *Blueprint, repoPath string, repoKind string) bool {
	// Check kind.
	if bp.AppliesTo.Kind != "any" && bp.AppliesTo.Kind != repoKind {
		return false
	}

	m := bp.AppliesTo.Match

	// Check has_path.
	if m.HasPath != "" {
		target := filepath.Join(repoPath, m.HasPath)
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return false
		}
	}

	// Check has_file.
	if m.HasFile != "" {
		target := filepath.Join(repoPath, m.HasFile)
		info, err := os.Stat(target)
		if err != nil || info.IsDir() {
			return false
		}
	}

	// Check name_like (simple glob on directory basename).
	if m.NameLike != "" {
		base := filepath.Base(repoPath)
		matched, err := filepath.Match(m.NameLike, base)
		if err != nil || !matched {
			return false
		}
	}

	return true
}

// FindMatchingBlueprints returns all blueprints that apply to the given repo.
func FindMatchingBlueprints(blueprints []*Blueprint, repoPath string, repoKind string) []*Blueprint {
	var matched []*Blueprint
	for _, bp := range blueprints {
		if Matches(bp, repoPath, repoKind) {
			matched = append(matched, bp)
		}
	}
	return matched
}

// ResolveGlob expands a glob pattern relative to a repo root and returns matching file paths.
func ResolveGlob(repoPath, pattern string) ([]string, error) {
	full := filepath.Join(repoPath, pattern)

	// filepath.Glob doesn't support ** natively. Handle simple cases.
	if strings.Contains(pattern, "**") {
		return resolveDoubleStarGlob(repoPath, pattern)
	}

	matches, err := filepath.Glob(full)
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// resolveDoubleStarGlob walks the directory tree to resolve ** patterns.
func resolveDoubleStarGlob(root, pattern string) ([]string, error) {
	// Split pattern at ** and match the suffix against all subdirs.
	parts := strings.SplitN(pattern, "**", 2)
	prefix := parts[0]
	suffix := ""
	if len(parts) > 1 {
		suffix = strings.TrimPrefix(parts[1], "/")
		suffix = strings.TrimPrefix(suffix, string(filepath.Separator))
	}

	baseDir := filepath.Join(root, prefix)
	var matches []string

	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return nil
		}
		if suffix == "" {
			matches = append(matches, path)
			return nil
		}
		// Check if the filename matches the suffix pattern.
		rel, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			return nil
		}
		matched, matchErr := filepath.Match(suffix, filepath.Base(rel))
		if matchErr != nil {
			return nil
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}
