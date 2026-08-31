package preflight

type Options struct{}

func DefaultChecks(_ Options) []Check {
	return []Check{
		NewDockerCheck(),
		NewDiskSpaceCheck(),
		NewIndexerReadinessCheck(),
		NewNetworkCheck(),
	}
}
