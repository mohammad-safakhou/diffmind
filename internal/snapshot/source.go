package snapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SourceResolver struct{}

func NewSourceResolver() SourceResolver {
	return SourceResolver{}
}

func (r SourceResolver) Prepare(ctx context.Context, source string, ref string) (PreparedSource, func() error, error) {
	if source == "" {
		return PreparedSource{}, nil, fmt.Errorf("source cannot be empty")
	}
	if ref == "" {
		ref = "HEAD"
	}

	if isRemoteSource(source) {
		return r.prepareRemote(ctx, source, ref)
	}
	return r.prepareLocal(ctx, source, ref)
}

func (r SourceResolver) prepareRemote(ctx context.Context, source string, ref string) (PreparedSource, func() error, error) {
	workdir, cleanup, err := createTempWorkspace()
	if err != nil {
		return PreparedSource{}, nil, err
	}

	if err := runGit(ctx, "clone", "--filter=blob:none", source, workdir); err != nil {
		_ = cleanup()
		return PreparedSource{}, nil, fmt.Errorf("git clone remote source: %w", err)
	}
	if err := runGit(ctx, "-C", workdir, "checkout", ref); err != nil {
		_ = cleanup()
		return PreparedSource{}, nil, fmt.Errorf("git checkout %q: %w", ref, err)
	}

	commit, err := gitOutput(ctx, "-C", workdir, "rev-parse", "HEAD")
	if err != nil {
		_ = cleanup()
		return PreparedSource{}, nil, fmt.Errorf("resolve commit sha: %w", err)
	}

	return PreparedSource{
		RepoLocator: source,
		Ref:         ref,
		CommitSHA:   strings.TrimSpace(commit),
		Workdir:     workdir,
		SourceType:  "remote",
	}, cleanup, nil
}

func (r SourceResolver) prepareLocal(ctx context.Context, source string, ref string) (PreparedSource, func() error, error) {
	absPath, err := filepath.Abs(source)
	if err != nil {
		return PreparedSource{}, nil, fmt.Errorf("resolve source path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return PreparedSource{}, nil, fmt.Errorf("stat source path: %w", err)
	}
	if !info.IsDir() {
		return PreparedSource{}, nil, fmt.Errorf("source path must be a directory")
	}

	isGitRepo := hasGitRepository(absPath)
	if !isGitRepo {
		if ref != "" && ref != "HEAD" {
			return PreparedSource{}, nil, fmt.Errorf("ref %q is unsupported for non-git local source", ref)
		}
		return PreparedSource{
			RepoLocator: absPath,
			Ref:         "WORKTREE",
			CommitSHA:   "",
			Workdir:     absPath,
			SourceType:  "local",
		}, noopCleanup, nil
	}

	if !gitHeadExists(ctx, absPath) {
		if ref != "" && ref != "HEAD" {
			return PreparedSource{}, nil, fmt.Errorf("ref %q is unsupported for git repo without commits", ref)
		}
		return PreparedSource{
			RepoLocator: absPath,
			Ref:         "WORKTREE",
			CommitSHA:   "",
			Workdir:     absPath,
			SourceType:  "local-uncommitted",
		}, noopCleanup, nil
	}

	workdir, cleanup, err := createTempWorkspace()
	if err != nil {
		return PreparedSource{}, nil, err
	}

	if err := runGit(ctx, "clone", "--local", "--filter=blob:none", absPath, workdir); err != nil {
		_ = cleanup()
		return PreparedSource{}, nil, fmt.Errorf("git clone local source: %w", err)
	}
	if err := runGit(ctx, "-C", workdir, "checkout", ref); err != nil {
		_ = cleanup()
		return PreparedSource{}, nil, fmt.Errorf("git checkout %q: %w", ref, err)
	}

	commit, err := gitOutput(ctx, "-C", workdir, "rev-parse", "HEAD")
	if err != nil {
		_ = cleanup()
		return PreparedSource{}, nil, fmt.Errorf("resolve commit sha: %w", err)
	}

	return PreparedSource{
		RepoLocator: absPath,
		Ref:         ref,
		CommitSHA:   strings.TrimSpace(commit),
		Workdir:     workdir,
		SourceType:  "local-git",
	}, cleanup, nil
}

func createTempWorkspace() (string, func() error, error) {
	dir, err := os.MkdirTemp("", "diffmind-snapshot-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp workspace: %w", err)
	}
	return dir, func() error { return os.RemoveAll(dir) }, nil
}

func noopCleanup() error {
	return nil
}

func hasGitRepository(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func isRemoteSource(source string) bool {
	if strings.HasPrefix(source, "git@") {
		return true
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "ssh://") {
		return true
	}
	return strings.HasSuffix(source, ".git")
}

func runGit(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "", errors.New("git output is empty")
	}
	return trimmed, nil
}

func gitHeadExists(ctx context.Context, repoPath string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "HEAD")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
