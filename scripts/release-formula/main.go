// release-formula renders a pinned Homebrew formula from the release checksums.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var platforms = []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"}

func render(version string, checksums io.Reader) ([]byte, error) {
	if !versionPattern.MatchString(version) {
		return nil, errors.New("version must be major.minor.patch with an optional prerelease suffix")
	}
	hashes := map[string]string{}
	scanner := bufio.NewScanner(checksums)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !hashPattern.MatchString(fields[0]) {
			return nil, errors.New("invalid checksum line")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if _, exists := hashes[name]; exists {
			return nil, fmt.Errorf("duplicate checksum: %s", name)
		}
		hashes[name] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, p := range platforms {
		if hashes["diffmind_"+version+"_"+p+".tar.gz"] == "" {
			return nil, fmt.Errorf("missing checksum for %s", p)
		}
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `# Generated from release checksums; do not replace these with mutable latest URLs.
class Diffmind < Formula
  desc "Deterministic cross-service architecture graphs for developers and agents"
  homepage "https://github.com/mohammad-safakhou/diffmind"
  version %q
  license "Apache-2.0"

`, version)
	for _, osname := range []string{"darwin", "linux"} {
		block := "on_macos"
		if osname == "linux" {
			block = "on_linux"
		}
		fmt.Fprintf(&out, "  %s do\n", block)
		for _, arch := range []string{"arm64", "amd64"} {
			cpu := "on_arm"
			if arch == "amd64" {
				cpu = "on_intel"
			}
			filename := "diffmind_" + version + "_" + osname + "_" + arch + ".tar.gz"
			fmt.Fprintf(&out, "    %s do\n      url %q\n      sha256 %q\n    end\n", cpu, "https://github.com/mohammad-safakhou/diffmind/releases/download/v"+version+"/"+filename, hashes[filename])
		}
		fmt.Fprint(&out, "  end\n\n")
	}
	fmt.Fprint(&out, `  depends_on "git"

  def install
    bin.install "diffmind"
  end

  test do
    ENV["DIFFMIND_HOME"] = (testpath/"workspace").to_s
    assert_match version.to_s, shell_output("#{bin}/diffmind version")
    assert_match '"ok":true', shell_output("#{bin}/diffmind doctor --json")
    system bin/"diffmind", "backup", "create", "--offline", "--output", testpath/"snapshot.tar.gz"
    system bin/"diffmind", "backup", "verify", "--archive", testpath/"snapshot.tar.gz"
  end
end
`)
	return out.Bytes(), nil
}

func main() {
	version := flag.String("version", "", "release version without v")
	checksums := flag.String("checksums", "", "checksums.txt path")
	output := flag.String("output", "", "new formula output path")
	flag.Parse()
	if *checksums == "" || *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "--version, --checksums, and --output are required")
		os.Exit(2)
	}
	f, err := os.Open(*checksums)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	body, err := render(*version, f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, err = out.Write(body)
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
