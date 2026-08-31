package pipeline

import "github.com/mohammad-safakhou/diffmind/internal/extractor/extraction"

type (
	repoFacts       = extraction.RepoFacts
	discoveryResult = extraction.DiscoveryResult
	detailJob       = extraction.DetailJob

	Result            = extraction.Result
	Failure           = extraction.Failure
	IntermediateState = extraction.IntermediateState
)
