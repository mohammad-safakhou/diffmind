package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AnalyzerIdentity identifies the actual executable, or the sources used by
// the development go-run fallback. A release version alone misses local builds.
func AnalyzerIdentity(binary string) (string, error) {
	name, _, dir := diffmindCommand(binary, nil)
	h := sha256.New()
	add := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		io.WriteString(h, path+"\x00")
		_, err = io.Copy(h, f)
		return err
	}
	if dir != "" {
		err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				if strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" || e.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") || e.Name() == "go.mod" || e.Name() == "go.sum" {
				return add(path)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	} else {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", err
		}
		if err := add(path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
