package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "expected.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func TestLoadExpectedRoundTrip(t *testing.T) {
	dir := writeFixture(t, `{
      "fixture":"x","repo_path":"repo",
      "exposures":[{"type":"http_route","details":{"method":"GET","path":"/a"},"deterministic":true}],
      "dependencies":[{"type":"db_operation","details":{"table":"a","operation":"read"}}],
      "connections":[{"from":{"type":"http_route","details":{"method":"GET","path":"/a"}},"to":{"type":"db_operation","details":{"table":"a","operation":"read"}}}]
    }`)
	set, err := LoadExpected(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if set.Fixture != "x" || len(set.Exposures) != 1 || len(set.Dependencies) != 1 || len(set.Connections) != 1 {
		t.Fatalf("unexpected set: %+v", set)
	}
	if got := set.ResolvedRepoPath(); got != filepath.Join(dir, "repo") {
		t.Fatalf("repo path: %q", got)
	}
}

func TestLoadExpectedRejectsMissingType(t *testing.T) {
	dir := writeFixture(t, `{"repo_path":"repo","exposures":[{"name":"x"}]}`)
	if _, err := LoadExpected(dir); err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestLoadExpectedRequiresRepoPath(t *testing.T) {
	dir := writeFixture(t, `{"fixture":"x"}`)
	if _, err := LoadExpected(dir); err == nil {
		t.Fatal("expected error for missing repo_path")
	}
}
