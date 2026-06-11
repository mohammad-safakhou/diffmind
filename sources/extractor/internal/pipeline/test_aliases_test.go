package pipeline

import (
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	"github.com/mohammad-safakhou/diffmind/internal/stage/repofacts"
)

// Test-only aliases preserve legacy characterization tests while production
// pipeline code calls the owning packages directly.

var (
	ClassifyError           = llmrun.ClassifyError
	ExtractHTTPStatus       = llmrun.ExtractHTTPStatus
	ShouldReportHTTPStatus  = llmrun.ShouldReportHTTPStatus
	BuildRepoFactsPrompt    = repofacts.BuildPrompt
	BuildDiscoveryPrompt    = extraction.BuildDiscoveryPrompt
	BuildDetailPrompt       = extraction.BuildDetailPrompt
	BuildReexaminePrompt    = extraction.BuildReexaminePrompt
	ScopeFrameworkPatterns  = extraction.ScopeFrameworkPatterns
	DetectedLanguageSet     = extraction.DetectedLanguageSet
	ConfirmedDiscoveryBlock = extraction.ConfirmedDiscoveryBlock
	Itoa                    = extraction.Itoa
	ForceObjectiveType      = extraction.ForceObjectiveType
	EnrichEntityGrouping    = extraction.EnrichEntityGrouping

	planDiscoveryShards        = discoverystage.PlanShards
	deterministicDBOperations  = discoverystage.DeterministicDBOperations
	entityFromFrameworkBinding = discoverystage.EntityFromFrameworkBinding
	mergeDiscoveryResults      = discoverystage.MergeDiscoveryResults

	shouldReexamine             = extraction.ShouldReexamine
	missingRequiredDetails      = extraction.MissingRequiredDetails
	discoverySemanticKey        = extraction.DiscoverySemanticKey
	isCompleteDeterministicSeed = extraction.IsCompleteDeterministicSeed
	hasDeterministicEvidence    = extraction.HasDeterministicEvidence
	splitMethodPath             = extraction.SplitMethodPath
	splitServiceMethod          = extraction.SplitServiceMethod
	hasDetailKey                = extraction.HasDetailKey
)

type symbolHint = extraction.SymbolHint
type bindingHint = extraction.BindingHint
