package analyzers

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func probeJdtls(root string) AdapterProbe {
	if !hasExtensions(root, ".java") {
		return AdapterProbe{Available: false, Reason: "no Java source files detected in source"}
	}

	bin := strings.TrimSpace(os.Getenv("DIFFMIND_JDTLS_BIN"))
	if bin == "" {
		bin = "jdtls"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return AdapterProbe{Available: false, Reason: fmt.Sprintf("jdtls binary not found (%s)", bin)}
	}

	ver, reason := probeToolVersion(path, "--version")
	if ver == "unknown" {
		ok, lspReason := probeJDTLSLSP(path, root, 12*time.Second)
		if !ok {
			return AdapterProbe{Available: false, Reason: fmt.Sprintf("jdtls probe failed: %s", lspReason)}
		}
		ver = "lsp-probed"
		reason = lspReason
	}
	return AdapterProbe{
		Available:            true,
		Reason:               fmt.Sprintf("jdtls available at %s (%s)", path, reason),
		ToolPath:             path,
		ToolVersion:          ver,
		ToolchainFingerprint: toolFingerprint(path, ver),
	}
}
