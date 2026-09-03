package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedFormula(t *testing.T) {
	var checksums strings.Builder
	for i, p := range platforms {
		fmt.Fprintf(&checksums, "%064x  diffmind_1.2.3_%s.tar.gz\n", i+1, p)
	}
	body, err := render("1.2.3", strings.NewReader(checksums.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range platforms {
		if !strings.Contains(string(body), "/download/v1.2.3/diffmind_1.2.3_"+p+".tar.gz") || !strings.Contains(string(body), fmt.Sprintf(`sha256 "%064x"`, i+1)) {
			t.Fatalf("platform %s missing", p)
		}
	}
	if strings.Contains(string(body), "/latest/") {
		t.Fatal("mutable release URL")
	}
	if ruby, err := exec.LookPath("ruby"); err == nil {
		p := filepath.Join(t.TempDir(), "diffmind.rb")
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(ruby, "-c", p).CombinedOutput(); err != nil {
			t.Fatalf("Ruby syntax: %v %s", err, out)
		}
		// Evaluate the conditional DSL for all release platforms without installing
		// anything or touching the developer's Homebrew taps/settings.
		dsl := `class Formula
  def self.desc(*) end
  def self.homepage(*) end
  def self.version(*) end
  def self.license(*) end
  def self.depends_on(*) end
  def self.test; end
  def self.on_macos; yield if ENV.fetch("FORMULA_PLATFORM").start_with?("darwin_"); end
  def self.on_linux; yield if ENV.fetch("FORMULA_PLATFORM").start_with?("linux_"); end
  def self.on_arm; yield if ENV.fetch("FORMULA_PLATFORM").end_with?("_arm64"); end
  def self.on_intel; yield if ENV.fetch("FORMULA_PLATFORM").end_with?("_amd64"); end
  def self.url(v); puts v; end
  def self.sha256(v); puts v; end
end
load ARGV.fetch(0)
`
		for i, platform := range platforms {
			cmd := exec.Command(ruby, "-e", dsl, p)
			cmd.Env = append(os.Environ(), "FORMULA_PLATFORM="+platform)
			out, err := cmd.CombinedOutput()
			want := fmt.Sprintf("https://github.com/mohammad-safakhou/diffmind/releases/download/v1.2.3/diffmind_1.2.3_%s.tar.gz\n%064x\n", platform, i+1)
			if err != nil || string(out) != want {
				t.Fatalf("%s dispatch: %v %s", platform, err, out)
			}
		}
	}
	for _, version := range []string{"v1.2.3", "1.2", `1.2.3";system("bad")`, "../evil"} {
		if _, err := render(version, strings.NewReader(checksums.String())); err == nil {
			t.Fatalf("unsafe version %q", version)
		}
	}
	for _, input := range []string{"", checksums.String() + checksums.String(), "invalid line", strings.Replace(checksums.String(), "darwin_arm64", "missing", 1)} {
		if _, err := render("1.2.3", strings.NewReader(input)); err == nil {
			t.Fatal("invalid checksums accepted")
		}
	}
}
