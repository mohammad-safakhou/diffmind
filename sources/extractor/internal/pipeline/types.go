package pipeline

import "github.com/mohammad-safakhou/diffmind/internal/extraction"

type (
	PathMapper     = extraction.PathMapper
	objectiveHints = extraction.ObjectiveHints

	repoFacts       = extraction.RepoFacts
	discoveryResult = extraction.DiscoveryResult
	detailJob       = extraction.DetailJob

	Result            = extraction.Result
	Failure           = extraction.Failure
	IntermediateState = extraction.IntermediateState
)
