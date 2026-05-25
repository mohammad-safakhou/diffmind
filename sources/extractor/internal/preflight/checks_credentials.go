package preflight

import (
	"context"
	"strings"
)

// CredentialsCheck verifies that the provider id + model id are
// populated. It does NOT actually attempt an LLM call — that would
// burn a token on every preflight cycle. Instead it confirms that
// the user has run `opencode auth login` (which is what populates
// these fields client-side).
//
// We split this from OpenCodeCheck so the UI can distinguish:
//   - server down (OpenCodeCheck=fail)         → start opencode
//   - server up, creds missing (Cred=fail)     → run `opencode auth login`
//   - server up, creds present (Cred=ok)       → ready to run
type CredentialsCheck struct {
	ProviderID string
	ModelID    string
}

// NewCredentialsCheck constructs a CredentialsCheck from the values
// configured on the Run form.
func NewCredentialsCheck(provider, model string) *CredentialsCheck {
	return &CredentialsCheck{ProviderID: provider, ModelID: model}
}

func (c *CredentialsCheck) Name() string  { return "credentials" }
func (c *CredentialsCheck) Title() string { return "Provider credentials" }

func (c *CredentialsCheck) Run(_ context.Context) Result {
	provider := strings.TrimSpace(c.ProviderID)
	model := strings.TrimSpace(c.ModelID)
	missing := []string{}
	if provider == "" {
		missing = append(missing, "provider_id")
	}
	if model == "" {
		missing = append(missing, "model_id")
	}
	if len(missing) > 0 {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityFail,
			Message:  "Missing " + strings.Join(missing, " + "),
			Remediation: "Run `opencode auth login` to populate the provider and model, then refresh the Run form. " +
				"You can also set them manually under the Run form's Advanced section.",
		}
	}
	return Result{
		Name:     c.Name(),
		Title:    c.Title(),
		Severity: SeverityOK,
		Message:  "Provider " + provider + " / model " + model,
	}
}
