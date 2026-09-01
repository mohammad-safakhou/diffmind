package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func createInstallablePack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	pack := validPack()
	pack.Tests = []TestCase{{
		Name: "basic", Fixture: "testdata/basic", Expected: ExpectedIdentity{ServiceName: "installed"},
	}}
	writePack(t, root, pack)
	if err := os.MkdirAll(filepath.Join(root, "testdata", "basic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "testdata", "basic", "service.yaml"), []byte("name: installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInstallLocalLockEnableAndTamperDetection(t *testing.T) {
	source := createInstallablePack(t)
	home := t.TempDir()
	first, err := Install(InstallOptions{Home: home, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Enabled || !strings.HasPrefix(first.Digest, "sha256:") {
		t.Fatalf("invalid lock entry: %+v", first)
	}
	second, err := Install(InstallOptions{Home: home, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("same content changed digest: %s != %s", first.Digest, second.Digest)
	}
	lock, err := ReadLock(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Packs) != 1 {
		t.Fatalf("reinstall duplicated lock entry: %+v", lock.Packs)
	}
	digestA, _ := LockDigest(lock)
	digestB, _ := LockDigest(lock)
	if digestA != digestB {
		t.Fatal("lock digest is nondeterministic")
	}
	if packs, err := LoadEnabled(home); err != nil || len(packs) != 1 {
		t.Fatalf("load enabled = %d, %v", len(packs), err)
	}
	if err := SetEnabled(home, first.ID, false); err != nil {
		t.Fatal(err)
	}
	if packs, err := LoadEnabled(home); err != nil || len(packs) != 0 {
		t.Fatalf("disabled pack loaded = %d, %v", len(packs), err)
	}
	if err := SetEnabled(home, first.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "tampered.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnabled(home); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampering was not detected: %v", err)
	}
}

func TestInstallFromGitPinsCommit(t *testing.T) {
	source := createInstallablePack(t)
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.email", "pack-test@example.invalid")
	runGit(t, source, "config", "user.name", "Pack Test")
	runGit(t, source, "add", ".")
	runGit(t, source, "commit", "-m", "initial pack")
	head := strings.TrimSpace(runGit(t, source, "rev-parse", "HEAD"))
	home := t.TempDir()
	installed, err := Install(InstallOptions{Home: home, Source: "file://" + source, Ref: head})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Revision != head {
		t.Fatalf("revision = %q, want %q", installed.Revision, head)
	}
}

func TestContentDigestRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ContentDigest(root); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
