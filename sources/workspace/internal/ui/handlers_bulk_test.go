package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubCloneURLTransport(t *testing.T) {
	repo := githubRepo{
		Name:     "svc",
		CloneURL: "https://github.example.com/acme/svc.git",
		SSHURL:   "git@github.example.com:acme/svc.git",
		HTMLURL:  "https://github.example.com/acme/svc",
	}

	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GH_TOKEN", "")
	if got := githubCloneURL(repo, importReposRequest{}); got != repo.CloneURL {
		t.Fatalf("auto with token = %q, want https clone url", got)
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", "")
	if got := githubCloneURL(repo, importReposRequest{}); got != repo.SSHURL {
		t.Fatalf("auto without token = %q, want ssh url", got)
	}
	if got := githubCloneURL(repo, importReposRequest{CloneTransport: "https"}); got != repo.CloneURL {
		t.Fatalf("https transport = %q, want https clone url", got)
	}
	if got := githubCloneURL(repo, importReposRequest{CloneTransport: "ssh"}); got != repo.SSHURL {
		t.Fatalf("ssh transport = %q, want ssh url", got)
	}
}

func TestImportCloneConcurrency(t *testing.T) {
	if got := importCloneConcurrency(0); got != 4 {
		t.Fatalf("default concurrency = %d, want 4", got)
	}
	if got := importCloneConcurrency(9); got != 9 {
		t.Fatalf("explicit concurrency = %d, want 9", got)
	}
	if got := importCloneConcurrency(99); got != 16 {
		t.Fatalf("capped concurrency = %d, want 16", got)
	}
}

func TestGitHubAuthHost(t *testing.T) {
	if got := githubAuthHost("https://github.company.com/acme/svc.git"); got != "github.company.com" {
		t.Fatalf("enterprise auth host = %q", got)
	}
	if got := githubAuthHost("git@github.company.com:acme/svc.git"); got != "" {
		t.Fatalf("ssh auth host = %q, want empty", got)
	}
	if got := githubAuthHost("https://gitlab.company.com/acme/svc.git"); got != "" {
		t.Fatalf("non-github auth host = %q, want empty", got)
	}
}

func TestGitHubTokenHost(t *testing.T) {
	tests := map[string]string{
		"https://api.github.com":                      "github.com",
		"https://github.com/example-internal/repo.git": "github.com",
		"https://github.company.com/api/v3":           "github.company.com",
		"https://gitlab.company.com/acme/svc.git":     "",
		"git@github.company.com:acme/svc.git":         "github.company.com",
	}
	for raw, want := range tests {
		if got := githubTokenHost(raw); got != want {
			t.Fatalf("githubTokenHost(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseGitHubReposResponseReportsGitHubErrorObject(t *testing.T) {
	_, err := parseGitHubReposResponse(404, "404 Not Found", []byte(`{"message":"Not Found","documentation_url":"https://docs.github.com/rest/repos/repos#list-organization-repositories"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "404 Not Found") || !strings.Contains(msg, "Not Found") || !strings.Contains(msg, "docs.github.com") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseGitHubReposResponseReportsUnexpectedObjectOnOK(t *testing.T) {
	_, err := parseGitHubReposResponse(200, "200 OK", []byte(`{"message":"Bad credentials"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseGitHubReposResponseAcceptsRepoArray(t *testing.T) {
	repos, err := parseGitHubReposResponse(200, "200 OK", []byte(`[{"name":"svc","clone_url":"https://github.com/acme/svc.git"}]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "svc" {
		t.Fatalf("unexpected repos: %+v", repos)
	}
}

func TestLocalReposFindsImmediateGitDirectories(t *testing.T) {
	root := t.TempDir()
	mkdirGit(t, filepath.Join(root, "svc-a"))
	mkdirGit(t, filepath.Join(root, "svc-b"))
	if err := os.Mkdir(filepath.Join(root, "not-a-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	repos, err := localRepos(importReposRequest{Root: root})
	if err != nil {
		t.Fatalf("localRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("repos = %+v, want 2", repos)
	}
	if repos[0].Name != "svc-a" || repos[1].Name != "svc-b" {
		t.Fatalf("unexpected repos: %+v", repos)
	}
}

func TestLocalReposRecursiveSkipsInsideRepo(t *testing.T) {
	root := t.TempDir()
	mkdirGit(t, filepath.Join(root, "group", "svc-a"))
	mkdirGit(t, filepath.Join(root, "group", "svc-a", "nested-ignored"))

	repos, err := localRepos(importReposRequest{Root: root, Recursive: true, MaxDepth: 3})
	if err != nil {
		t.Fatalf("localRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "svc-a" {
		t.Fatalf("unexpected repos: %+v", repos)
	}
}

func mkdirGit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}
