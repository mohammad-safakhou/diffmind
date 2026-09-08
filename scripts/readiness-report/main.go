// Command readiness-report runs the local public-beta candidate gates and
// writes a machine-readable evidence record. It never publishes artifacts.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type step struct {
	Name         string   `json:"name"`
	Command      []string `json:"command"`
	Status       string   `json:"status"`
	DurationMS   int64    `json:"duration_ms"`
	OutputSHA256 string   `json:"output_sha256"`
}
type report struct {
	Schema       string    `json:"schema"`
	CreatedAt    time.Time `json:"created_at"`
	Commit       string    `json:"commit"`
	Dirty        bool      `json:"dirty"`
	Platform     string    `json:"platform"`
	GoVersion    string    `json:"go_version"`
	CorpusDigest string    `json:"corpus_digest"`
	PackDigest   string    `json:"pack_digest"`
	BinaryDigest string    `json:"binary_digest,omitempty"`
	OK           bool      `json:"ok"`
	Steps        []step    `json:"steps"`
}

func main() {
	output := flag.String("output", "", "optional JSON report path")
	binary := flag.String("binary", "", "optional installed candidate binary")
	flag.Parse()
	r := report{Schema: "diffmind.readiness.v1", CreatedAt: time.Now().UTC(), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), OK: true}
	r.Commit = commandText("git", "rev-parse", "HEAD")
	r.Dirty = commandText("git", "status", "--porcelain") != ""
	r.CorpusDigest = treeDigest([]string{"internal", "protocol", "indexerbuild"}, "_test.go")
	r.PackDigest = treeDigest([]string{"packs"}, "")
	if *binary != "" {
		r.BinaryDigest = fileDigest(*binary)
	}
	commands := [][]string{
		{"go", "test", "./..."},
		{"go", "test", "-race", "./..."},
		{"go", "vet", "./..."},
		{"go", "run", "./cmd/diffmind", "pack", "lint", "./packs"},
		{"go", "run", "./cmd/diffmind", "pack", "test", "./packs"},
		{"npm", "--prefix", "internal/workspace/ui/web", "test"},
		{"npm", "--prefix", "internal/extractor/ui/web", "test"},
		{"npm", "--prefix", "internal/workspace/ui/web", "run", "build"},
		{"npm", "--prefix", "internal/extractor/ui/web", "run", "build"},
	}
	for _, argv := range commands {
		started := time.Now()
		var captured bytes.Buffer
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout = io.MultiWriter(os.Stdout, &captured)
		cmd.Stderr = io.MultiWriter(os.Stderr, &captured)
		err := cmd.Run()
		status := "passed"
		if err != nil {
			status = "failed"
			r.OK = false
		}
		sum := sha256.Sum256(captured.Bytes())
		r.Steps = append(r.Steps, step{Name: argv[0] + " " + argv[1], Command: argv, Status: status, DurationMS: time.Since(started).Milliseconds(), OutputSHA256: hex.EncodeToString(sum[:])})
	}
	body, _ := json.MarshalIndent(r, "", "  ")
	body = append(body, '\n')
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(*output, body, 0o644); err != nil {
			panic(err)
		}
	}
	_, _ = os.Stdout.Write(body)
	if !r.OK {
		os.Exit(1)
	}
}

func commandText(name string, args ...string) string {
	body, _ := exec.Command(name, args...).Output()
	return string(bytes.TrimSpace(body))
}
func fileDigest(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func treeDigest(roots []string, suffix string) string {
	var paths []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (suffix == "" || strings.HasSuffix(filepath.Base(path), suffix)) {
				paths = append(paths, path)
			}
			return nil
		})
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err == nil {
			fmt.Fprintf(h, "%s\x00", filepath.ToSlash(path))
			_, _ = h.Write(body)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
