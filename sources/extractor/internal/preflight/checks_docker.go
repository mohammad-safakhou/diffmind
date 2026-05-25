package preflight

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// DockerCheck verifies that the Docker daemon is reachable. We
// invoke `docker version --format json` and look at the exit code.
//
// Failure modes we care about:
//   - The `docker` CLI is not on PATH (DockerNotInstalled).
//   - The CLI is present but the daemon is not running
//     (DockerDaemonDown).
//   - The CLI is present, daemon is up, but `docker version`
//     itself fails for some other reason (e.g. permissions).
//
// On any failure we emit SeverityFail because the index stage
// cannot proceed without Docker; the user explicitly asked for
// hard-rejection.
type DockerCheck struct {
	// CommandPath is the docker binary; defaults to "docker".
	CommandPath string
}

// NewDockerCheck constructs a DockerCheck with the default binary.
func NewDockerCheck() *DockerCheck { return &DockerCheck{} }

func (c *DockerCheck) Name() string  { return "docker" }
func (c *DockerCheck) Title() string { return "Docker daemon" }

func (c *DockerCheck) Run(ctx context.Context) Result {
	bin := c.CommandPath
	if bin == "" {
		bin = "docker"
	}

	// Resolve PATH first so we can emit a precise "not installed"
	// message instead of the generic exec failure.
	if _, err := exec.LookPath(bin); err != nil {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityFail,
			Message:  "Docker CLI not found on PATH",
			Detail:   err.Error(),
			Remediation: "Install Docker Desktop (macOS/Windows) or docker.io / colima " +
				"(Linux/macOS) and ensure the `docker` binary is on PATH.",
		}
	}

	cmd := exec.CommandContext(ctx, bin, "version", "--format", "{{.Server.Version}}")
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		// Distinguish "daemon not running" from "exec failed".
		// Docker emits a specific error message in this case.
		msg := "Docker daemon is not responding"
		rem := "Start the Docker daemon (Docker Desktop, colima, or `sudo systemctl start docker`) and try again."
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = "Docker probe timed out"
			rem = "The daemon is reachable but slow. Restart Docker or check `docker info` manually."
		} else if strings.Contains(strings.ToLower(trimmed), "cannot connect to the docker daemon") {
			msg = "Cannot connect to the Docker daemon"
		}
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityFail,
			Message:     msg,
			Detail:      trimmed,
			Remediation: rem,
		}
	}

	if trimmed == "" {
		// `docker version` exited 0 but returned no server version,
		// which means we hit the CLI without a working daemon.
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityFail,
			Message:     "Docker server version reported empty",
			Detail:      "docker version returned an empty Server.Version field",
			Remediation: "Ensure the Docker daemon is fully started.",
		}
	}

	return Result{
		Name:     c.Name(),
		Title:    c.Title(),
		Severity: SeverityOK,
		Message:  "Docker " + trimmed + " reachable",
	}
}
