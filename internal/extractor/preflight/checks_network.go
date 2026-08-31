package preflight

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// NetworkCheck probes outbound network reachability by issuing a
// HEAD request to a well-known endpoint. Without network, the
// indexer's cold base image build cannot pull JDK / Node / Python /
// .NET tarballs from upstream registries, and Maven / npm /
// pip / NuGet inside the container can't resolve dependencies.
//
// We emit SeverityWarn (not Fail) because:
//   - A run can still succeed if the base images we need are
//     already cached locally;
//   - Some users run DiffMind behind a corporate proxy that
//     blocks github.com but allows their internal registries;
//   - The dashboard should not refuse to launch in those
//     environments.
//
// We probe two URLs and treat success on either as "OK" so a
// flaky DNS server doesn't false-flag.
type NetworkCheck struct {
	URLs    []string
	Timeout time.Duration
}

// NewNetworkCheck constructs a NetworkCheck with two unrelated
// probe targets so a single registry outage doesn't trip us.
func NewNetworkCheck() *NetworkCheck {
	return &NetworkCheck{
		URLs: []string{
			"https://github.com",
			"https://www.cloudflare.com",
		},
	}
}

func (c *NetworkCheck) Name() string  { return "network" }
func (c *NetworkCheck) Title() string { return "Outbound network" }

func (c *NetworkCheck) Run(ctx context.Context) Result {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	tried := []string{}
	var lastErr string
	for _, u := range c.URLs {
		tried = append(tried, u)
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 500 {
			return Result{
				Name:     c.Name(),
				Title:    c.Title(),
				Severity: SeverityOK,
				Message:  "Reachable (" + u + ")",
			}
		}
		lastErr = "HTTP " + resp.Status
	}
	return Result{
		Name:     c.Name(),
		Title:    c.Title(),
		Severity: SeverityWarn,
		Message:  "No outbound connectivity detected",
		Detail:   "Tried " + strings.Join(tried, ", ") + " - last error: " + lastErr,
		Remediation: "If you are behind a corporate proxy, set HTTPS_PROXY / NO_PROXY before launching `diffmind ui`. " +
			"DiffMind can still run if all required indexer base images are already cached locally.",
	}
}
