package analyzers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func probeTsserver(root string) AdapterProbe {
	if !hasExtensions(root, ".ts", ".tsx", ".js", ".jsx") {
		return AdapterProbe{Available: false, Reason: "no TypeScript/JavaScript source files detected in source"}
	}
	configuredBin := strings.TrimSpace(os.Getenv("DIFFMIND_TSSERVER_BIN"))
	bin := configuredBin
	if bin == "" {
		// Use the LSP wrapper by default. The raw `tsserver` binary is not LSP.
		bin = "typescript-language-server"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		if configuredBin == "" {
			return AdapterProbe{Available: false, Reason: "typescript-language-server binary not found (install with npm i -g typescript-language-server)"}
		}
		return AdapterProbe{Available: false, Reason: fmt.Sprintf("tsserver binary not found (%s)", bin)}
	}
	ver, reason := probeToolVersion(path, "--version")
	// Preserve env override compatibility for tests/custom wrappers.
	if configuredBin == "" {
		ok, lspReason := probeTsserverLSP(path, root, 12*time.Second)
		if !ok {
			return AdapterProbe{Available: false, Reason: fmt.Sprintf("typescript lsp probe failed: %s", lspReason)}
		}
		if ver == "unknown" {
			ver = "lsp-probed"
		}
		reason = lspReason
	}
	return AdapterProbe{
		Available:            true,
		Reason:               fmt.Sprintf("tsserver available at %s (%s)", path, reason),
		ToolPath:             path,
		ToolVersion:          ver,
		ToolchainFingerprint: toolFingerprint(path, ver),
	}
}

func probePyright(root string) AdapterProbe {
	if !hasExtensions(root, ".py") {
		return AdapterProbe{Available: false, Reason: "no Python source files detected in source"}
	}
	bin := strings.TrimSpace(os.Getenv("DIFFMIND_PYRIGHT_BIN"))
	if bin == "" {
		bin = "pyright-langserver"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return AdapterProbe{Available: false, Reason: fmt.Sprintf("pyright binary not found (%s)", bin)}
	}
	ver, reason := probeToolVersion(path, "--version")
	if ver == "unknown" {
		ok, lspReason := probePyrightLSP(path, root, 10*time.Second)
		if !ok {
			return AdapterProbe{Available: false, Reason: fmt.Sprintf("pyright probe failed: %s", lspReason)}
		}
		ver = "lsp-probed"
		reason = lspReason
	}
	return AdapterProbe{
		Available:            true,
		Reason:               fmt.Sprintf("pyright available at %s (%s)", path, reason),
		ToolPath:             path,
		ToolVersion:          ver,
		ToolchainFingerprint: toolFingerprint(path, ver),
	}
}

func probeToolVersion(path string, arg string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, arg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown", "version probe failed"
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "unknown", "version output empty"
	}
	line := strings.Split(text, "\n")[0]
	return line, "version probe ok"
}

func toolFingerprint(path string, version string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(path)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.TrimSpace(version)))
	return hex.EncodeToString(h.Sum(nil))
}

func hasExtensions(root string, exts ...string) bool {
	if root == "" {
		return false
	}
	set := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		e := strings.ToLower(strings.TrimSpace(ext))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		set[e] = struct{}{}
	}
	if len(set) == 0 {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".diffmind", "node_modules", "vendor", "bin", ".gocache", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := set[strings.ToLower(filepath.Ext(name))]; ok {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
