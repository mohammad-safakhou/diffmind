package analyzers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
		if strings.EqualFold(bin, "gopls") {
			if fallback := lookupGoBinTool("gopls"); fallback != "" {
				path = fallback
			} else {
				return AdapterProbe{Available: false, Reason: fmt.Sprintf("gopls binary not found (%s)", bin)}
			}
		} else {
			return AdapterProbe{Available: false, Reason: fmt.Sprintf("gopls binary not found (%s)", bin)}
		}
	}

	ver, reason := probeToolVersion(path, "version")
	if ver == "unknown" {
		ok, lspReason := probeGoplsLSP(path, root, 10*time.Second)
		if !ok {
			return AdapterProbe{Available: false, Reason: fmt.Sprintf("gopls probe failed: %s", lspReason)}
		}
		ver = "lsp-probed"
		reason = lspReason
	}
	return AdapterProbe{
		Available:            true,
		Reason:               fmt.Sprintf("gopls available at %s (%s)", path, reason),
		ToolPath:             path,
		ToolVersion:          ver,
		ToolchainFingerprint: toolFingerprint(path, ver),
	}
}

func lookupGoBinTool(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	gopath := strings.TrimSpace(string(out))
	if gopath == "" {
		return ""
	}
	candidate := filepath.Join(gopath, "bin", name)
	st, statErr := os.Stat(candidate)
	if statErr != nil || st.IsDir() {
		return ""
	}
	if st.Mode()&0o111 == 0 {
		return ""
	}
	return candidate
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
