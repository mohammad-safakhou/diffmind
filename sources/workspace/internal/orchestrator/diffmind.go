package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// DiffMindRunOptions mirrors the diffmind run CLI flags DiffMind exposes.
// Pointer fields are used where zero is a meaningful explicit value.
type DiffMindRunOptions struct {
	Pipeline                string   `json:"pipeline,omitempty"`
	ConfigPath              string   `json:"config_path,omitempty"`
	OutDir                  string   `json:"out_dir,omitempty"`
	LogFile                 string   `json:"log_file,omitempty"`
	Workers                 int      `json:"workers,omitempty"`
	MaxCatalogItems         int      `json:"max_catalog_items,omitempty"`
	MinConfidence           *float64 `json:"min_confidence,omitempty"`
	OpenCodeURL             string   `json:"opencode_url,omitempty"`
	OpenCodeUsername        string   `json:"opencode_username,omitempty"`
	OpenCodePassword        string   `json:"opencode_password,omitempty"`
	OpenCodeTimeoutSeconds  int      `json:"opencode_timeout_seconds,omitempty"`
	ProviderID              string   `json:"provider_id,omitempty"`
	ModelID                 string   `json:"model_id,omitempty"`
	ModelVariant            string   `json:"model_variant,omitempty"`
	CleanupOpenCodeSessions bool     `json:"cleanup_opencode_sessions,omitempty"`
	OpenCodeDeleteDelaySec  int      `json:"opencode_delete_delay_seconds,omitempty"`
	ReuseOpenCodeSession    bool     `json:"reuse_opencode_session,omitempty"`
	SkipReexamination       bool     `json:"skip_reexamination,omitempty"`
	DiscoveryVerify         bool     `json:"discovery_verify,omitempty"`
	DiscoveryVerifyMode     string   `json:"discovery_verify_mode,omitempty"`
	DiscoveryVerifySamples  int      `json:"discovery_verify_samples,omitempty"`
	DiscoveryFrameworkScope bool     `json:"discovery_framework_scope,omitempty"`
	IdleTimeoutSeconds      int      `json:"idle_timeout_seconds,omitempty"`
	PromptRetryCount        *int     `json:"prompt_retry_count,omitempty"`
	MaxCallSeconds          int      `json:"max_call_seconds,omitempty"`
	LivenessPollSeconds     int      `json:"liveness_poll_seconds,omitempty"`
	Verbose                 bool     `json:"verbose,omitempty"`
	Trace                   bool     `json:"trace,omitempty"`
}

// RunDiffMind triggers a DiffMind run against a repository.
func RunDiffMind(binaryPath, repoPath string, opts DiffMindRunOptions, log *util.Logger) error {
	args := opts.Args(repoPath)
	log.Info("running DiffMind", "binary", binaryPath, "repo", repoPath)
	cmdName, cmdArgs, cmdDir := diffmindCommand(binaryPath, args)
	cmd := exec.Command(cmdName, cmdArgs...)
	if cmdDir != "" {
		cmd.Dir = cmdDir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("diffmind run failed: %w\noutput: %s", err, string(output))
	}
	log.Info("DiffMind run complete", "repo", repoPath)
	return nil
}

func (o DiffMindRunOptions) Args(repoPath string) []string {
	pipeline := o.Pipeline
	if pipeline == "" {
		pipeline = "deterministic"
	}
	args := []string{"run", "--repo", repoPath, "--pipeline", pipeline}
	addString := func(flag, value string) {
		if value != "" {
			args = append(args, flag, value)
		}
	}
	addInt := func(flag string, value int) {
		if value > 0 {
			args = append(args, flag, fmt.Sprint(value))
		}
	}
	addBool := func(flag string, value bool) {
		if value {
			args = append(args, flag)
		}
	}
	addString("--config", o.ConfigPath)
	addString("--out", o.OutDir)
	addString("--log-file", o.LogFile)
	addInt("--workers", o.Workers)
	addInt("--max-catalog-items", o.MaxCatalogItems)
	if o.MinConfidence != nil {
		args = append(args, "--min-confidence", fmt.Sprint(*o.MinConfidence))
	}
	addString("--opencode-url", o.OpenCodeURL)
	addString("--opencode-username", o.OpenCodeUsername)
	addString("--opencode-password", o.OpenCodePassword)
	addInt("--opencode-timeout-seconds", o.OpenCodeTimeoutSeconds)
	addString("--provider-id", o.ProviderID)
	addString("--model-id", o.ModelID)
	addString("--model-variant", o.ModelVariant)
	addBool("--cleanup-opencode-sessions", o.CleanupOpenCodeSessions)
	addInt("--opencode-delete-delay-seconds", o.OpenCodeDeleteDelaySec)
	addBool("--reuse-opencode-session", o.ReuseOpenCodeSession)
	addBool("--skip-reexamination", o.SkipReexamination)
	addBool("--discovery-verify", o.DiscoveryVerify)
	addString("--discovery-verify-mode", o.DiscoveryVerifyMode)
	addInt("--discovery-verify-samples", o.DiscoveryVerifySamples)
	addBool("--discovery-framework-scope", o.DiscoveryFrameworkScope)
	addInt("--idle-timeout-seconds", o.IdleTimeoutSeconds)
	if o.PromptRetryCount != nil {
		args = append(args, "--prompt-retry-count", fmt.Sprint(*o.PromptRetryCount))
	}
	addInt("--max-call-seconds", o.MaxCallSeconds)
	addInt("--liveness-poll-seconds", o.LivenessPollSeconds)
	addBool("--verbose", o.Verbose)
	addBool("--trace", o.Trace)
	return args
}

func diffmindCommand(binaryPath string, args []string) (string, []string, string) {
	if binaryPath != "" && binaryPath != "diffmind" {
		return binaryPath, args, ""
	}
	if sourceDir := localDiffMindSourceDir(); sourceDir != "" {
		goArgs := append([]string{"run", "./cmd/diffmind"}, args...)
		return "go", goArgs, sourceDir
	}
	if binaryPath == "" {
		binaryPath = "diffmind"
	}
	return binaryPath, args, ""
}

func localDiffMindSourceDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(filepath.Dir(dir), "diffmind", "cmd", "diffmind", "main.go")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return filepath.Dir(filepath.Dir(filepath.Dir(candidate)))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}
