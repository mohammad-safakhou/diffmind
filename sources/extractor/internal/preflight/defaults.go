package preflight

import (
	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// Options bundles the inputs we need to construct the full set of
// production checks. We package them in a struct so adding a new
// check that needs a new input doesn't ripple through every caller.
type Options struct {
	// OpenCodeURL is the configured OpenCode base URL. Empty
	// disables the OpenCodeCheck (it emits warn instead of fail
	// so the dashboard doesn't refuse to launch before the user
	// has filled in the form).
	OpenCodeURL string
	// OpenCodeUser and OpenCodePass are the credentials used for
	// the reachability probe.
	OpenCodeUser string
	OpenCodePass string
	// ProviderID and ModelID feed CredentialsCheck.
	ProviderID string
	ModelID    string
}

// DefaultChecks builds the production check set. The order doesn't
// matter for execution (parallel) but does control the order in
// which Results appear in the JSON Report (sorted by Name).
func DefaultChecks(opts Options) []Check {
	return []Check{
		NewDockerCheck(),
		NewOpenCodeCheck(opts.OpenCodeURL, opts.OpenCodeUser, opts.OpenCodePass),
		NewCredentialsCheck(opts.ProviderID, opts.ModelID),
		NewDiskSpaceCheck(),
		NewIndexerReadinessCheck(),
		NewNetworkCheck(),
		NewSnapshotWritableCheck(),
	}
}

// OptionsFromConfig is a small helper that extracts an Options
// struct from a config.Config so callers don't have to know which
// fields map where. The dashboard's UI server builds Options from
// the live form values (which are not in cfg yet at preflight
// time), while app.Run builds Options from cfg directly.
func OptionsFromConfig(cfg config.Config) Options {
	return Options{
		OpenCodeURL:  cfg.OpenCode.BaseURL,
		OpenCodeUser: cfg.OpenCode.Username,
		OpenCodePass: cfg.OpenCode.Password,
		ProviderID:   cfg.OpenCode.ProviderID,
		ModelID:      cfg.OpenCode.ModelID,
	}
}
