package preflight

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenCodeCheck verifies that the OpenCode server is reachable by
// hitting its /global/health endpoint. We deliberately do NOT
// require credentials here — bare reachability is enough for the
// dashboard's System Status panel. CredentialsCheck handles the
// auth/provider/model question separately so the UI can distinguish
// "OpenCode is down" from "creds are stale".
type OpenCodeCheck struct {
	// URL is the OpenCode base URL, e.g. http://127.0.0.1:4096.
	// Empty disables the check (returns SeverityWarn so the UI
	// shows amber until the user fills the form).
	URL string
	// Username and Password may be set; if present we send Basic
	// auth so we exercise the same code path the real run will.
	Username string
	Password string
	// Timeout overrides the http.Client timeout. Defaults to 3s.
	Timeout time.Duration
}

// NewOpenCodeCheck constructs an OpenCodeCheck with sensible
// defaults.
func NewOpenCodeCheck(url, user, pass string) *OpenCodeCheck {
	return &OpenCodeCheck{URL: url, Username: user, Password: pass}
}

func (c *OpenCodeCheck) Name() string  { return "opencode" }
func (c *OpenCodeCheck) Title() string { return "OpenCode server" }

func (c *OpenCodeCheck) Run(ctx context.Context) Result {
	if strings.TrimSpace(c.URL) == "" {
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityWarn,
			Message:     "OpenCode URL not configured",
			Remediation: "Set the OpenCode URL on the Run form (or with --opencode-url).",
		}
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	u := strings.TrimRight(c.URL, "/") + "/global/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityFail,
			Message:     "Failed to construct OpenCode probe",
			Detail:      err.Error(),
			Remediation: "Check the OpenCode URL is a valid URL.",
		}
	}
	if c.Username != "" || c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityFail,
			Message:  "OpenCode server unreachable",
			Detail:   err.Error(),
			Remediation: "Start the OpenCode server (`opencode serve` or your launchd / systemd unit) " +
				"and verify the URL on the Run form matches.",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityFail,
			Message:  "OpenCode rejected credentials (401)",
			Detail:   fmt.Sprintf("HTTP %d at %s", resp.StatusCode, u),
			Remediation: "Check the Username / Password fields on the Run form match what OpenCode expects " +
				"(run `opencode serve --print-config` to inspect the credentials).",
		}
	}
	if resp.StatusCode >= 400 {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityFail,
			Message:  "OpenCode returned " + resp.Status,
			Detail:   fmt.Sprintf("HTTP %d at %s", resp.StatusCode, u),
		}
	}

	return Result{
		Name:     c.Name(),
		Title:    c.Title(),
		Severity: SeverityOK,
		Message:  "OpenCode reachable at " + c.URL,
	}
}
