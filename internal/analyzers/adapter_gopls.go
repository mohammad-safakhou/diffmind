package analyzers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func probeGopls(root string) AdapterProbe {
	if !hasGoModuleOrSource(root) {
		return AdapterProbe{Available: false, Reason: "no go.mod or .go files detected in source"}
	}

	bin := strings.TrimSpace(os.Getenv("DIFFMIND_GOPLS_BIN"))
	if bin == "" {
		bin = "gopls"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return AdapterProbe{Available: false, Reason: fmt.Sprintf("gopls binary not found (%s)", bin)}
	}

	ver, reason := probeToolVersion(path, "version")
	if ver == "unknown" {
		return AdapterProbe{Available: false, Reason: fmt.Sprintf("gopls probe failed: %s", reason)}
	}
	return AdapterProbe{
		Available:            true,
		Reason:               fmt.Sprintf("gopls available at %s (%s)", path, reason),
		ToolPath:             path,
		ToolVersion:          ver,
		ToolchainFingerprint: toolFingerprint(path, ver),
	}
}

func hasGoModuleOrSource(root string) bool {
	if root == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		return true
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".diffmind", "node_modules", "vendor", "bin", ".gocache":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(name), ".go") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
